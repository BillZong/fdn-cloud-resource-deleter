package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/relvacode/iso8601"
	"github.com/yaa110/sslice"
	cli "gopkg.in/urfave/cli.v1"
)

const (
	configFilePathLongFlag = "config"
	nodeCountLongFlag      = "node-count"
)

const (
	defaultTemplatePath = "./node-deleter-configs.yaml"
	templateToShow      = `cluster-type: "fixed" # fixed or dynamic. fixed one should provide existed node (not deleted from OpenWhisk cluster yet) list. dynamic one would use cloud node handle process.
fixed:
  ssh-port: 12345
  user-name: "root"
  ssh-key-file: "./key-20191106" # use private key
  password: "123456Abc" # use password
  nodes:
    - inner-ip: "172.17.0.2"
      host-name: "a"
    - inner-ip: "172.17.0.3"
      host-name: "b"
    - inner-ip: "172.17.0.4"
      host-name: "c"
dynamic:
  cloud-provider: "aliyun"
  aliyun:
    # Required Parameters.
    # region id devided by aliyun
    region-id: "cn-shenzhen"
    # user acccess key id, might be RAM user
    access-key-id: "123456abcdef"
    # user access key secret
    access-key-secret: "asdfasdfasdf"
    # vpc id 
    vpc-id: "vpc-abcdefg"
    # Optional Parameters.
    # ssh port, default 22
    ssh-port: 12345
    # ssh private key path, no need when use password login
    ssh-key-file: "./key-20191106"
    # password, no need when use ssh private key login
    password: "123456Abc"
    # # delete strategy, "oldest"/"newest". When no set, no strategy taken
    # delete-strategy: "oldest"
    # Debug Parameters
    # Debug mode. default false
    debug: false
# Command Line Parameters. Could be used in yaml, too
# # node count that want to be delete. default 1
# node-count:
#   1`
)

func main() {
	app := cli.NewApp()

	app.Name = "node-deleter"
	app.Version = "0.1.0"
	app.Description = "Tools for deleting invoker nodes from OpenWhisk cluster. First develeopped and used in FDN. Currently support fixed-number-nodes cluster, and aliyun ecs."
	app.Authors = []cli.Author{
		{Name: "Bill Zong", Email: "billzong@163.com"},
	}

	app.Commands = []cli.Command{
		{
			Name:  "template",
			Usage: "options for config yaml template",
			Subcommands: []cli.Command{
				{
					Name:   "show",
					Usage:  "show the template",
					Action: showTemplate,
				},
				{
					Name:  "create",
					Usage: "create (or cover) the tempalte to the path",
					Flags: []cli.Flag{
						cli.StringFlag{
							Name:  "path,p",
							Usage: "path for the config file, must have.",
							Value: defaultTemplatePath,
						},
					},
					Action: createTemplate,
				},
			},
		},
	}
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "config,c",
			Usage: "config file path, must have.",
			Value: "./node-joiner-configs.yaml",
		},
		cli.IntFlag{
			Name:  nodeCountLongFlag,
			Usage: "node count that want to be join",
			Value: 1,
		},
	}
	app.Action = deleteFromOWCluster

	err := app.Run(os.Args)
	if err != nil {
		panic(err)
	}
}

func createTemplate(ctx *cli.Context) error {
	path := ctx.String("path")
	if len(path) == 0 {
		path = defaultTemplatePath
	}
	return ioutil.WriteFile(path, []byte(templateToShow), 0644)
}

func showTemplate(ctx *cli.Context) error {
	fmt.Println("You could use this config yaml template:\n", templateToShow)
	return nil
}

type NodeInfo struct {
	InnerIP  string `yaml:"inner-ip"`
	HostName string `yaml:"host-name"`
}

type FixedNodeConfig struct {
	SSHPort    int         `yaml:"ssh-port,omitempty"`
	UserName   string      `yaml:"user-name,omitempty"`
	SSHKeyFile *string     `yaml:"ssh-key-file,omitempty"`
	Password   *string     `yaml:"password,omitempty"`
	Nodes      []*NodeInfo `yaml:"nodes"`
}

type AliyunEcsConfig struct {
	RegionID       string  `yaml:"region-id"`
	AccessID       string  `yaml:"access-key-id"`
	AccessSecret   string  `yaml:"access-key-secret"`
	VpcID          string  `yaml:"vpc-id"`
	SSHPort        *int    `yaml:"ssh-port,omitempty"`
	SSHKeyFile     *string `yaml:"ssh-key-file,omitempty"`
	Password       *string `yaml:"password,omitempty"`
	DeleteStrategy *string `yaml:"delete-strategy,omitempty"`
	Debug          *bool   `yaml:"debug,omitempty"`
}

type DynamicNodeConfig struct {
	CloudProvider string           `yaml:"cloud-provider"`
	AliyunConfig  *AliyunEcsConfig `yaml:"aliyun,omitempty"`
}

type TopLevelConfigs struct {
	ClusterType   string             `yaml:"cluster-type"`
	FixedConfig   *FixedNodeConfig   `yaml:"fixed,omitempty"`
	DynamicConfig *DynamicNodeConfig `yaml:"dynamic,omitempty"`
	NodeCount     *int               `yaml:"node-count,omitempty"`
}

func deleteFromOWCluster(ctx *cli.Context) error {
	var cfg = TopLevelConfigs{
		ClusterType: "fixed",
		FixedConfig: &FixedNodeConfig{
			SSHPort:  22,
			UserName: "root",
		},
	}
	configPath := ctx.String(configFilePathLongFlag)
	if len(configPath) == 0 {
		return fmt.Errorf("config file not existed")
	}
	if err := ReadYamlFile(configPath, &cfg); err != nil {
		return err
	}
	if cfg.NodeCount == nil {
		nodeCount := ctx.Int(nodeCountLongFlag)
		cfg.NodeCount = &nodeCount
	}

	if cfg.ClusterType == "fixed" {
		return handleFixedConfigs(cfg.FixedConfig, *cfg.NodeCount)
	} else if cfg.ClusterType == "dynamic" {
		if cfg.DynamicConfig.CloudProvider != "aliyun" {
			return fmt.Errorf("cloud provider (%v) not supported yet", cfg.DynamicConfig.CloudProvider)
		}
		return handleAliyunECSConfigs(cfg.DynamicConfig.AliyunConfig, *cfg.NodeCount)
	} else {
		return fmt.Errorf("cluster type (%v) not supported yet", cfg.ClusterType)
	}
}

func contains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

func handleFixedConfigs(cfg *FixedNodeConfig, nodeCount int) error {
	// got current nodes
	output, err := exec.Command("bash", "-c", "kubectl get no --show-labels | grep \"openwhisk-role=invoker\" | awk '{print $1}'").Output()
	if err != nil {
		fmt.Printf("kubectl failed: %v", err.Error())
		return err
	}
	currentNodeNames := strings.Split(string(output), "\n")

	// find intersection of the configuration and current node set
	count := 0
	targetNodes := make([]*NodeInfo, 0)
	for _, node := range cfg.Nodes {
		if count >= nodeCount {
			break
		}
		if contains(currentNodeNames, node.HostName) {
			targetNodes = append(targetNodes, node)
			count++
		}
	}

	// delete nodes
	return deleteInstancesFromOWCluster(targetNodes, cfg.SSHPort, cfg.UserName, cfg.SSHKeyFile, cfg.Password)
}

func handleAliyunECSConfigs(cfg *AliyunEcsConfig, nodeCount int) error {
	client, err := ecs.NewClientWithAccessKey(cfg.RegionID, cfg.AccessID, cfg.AccessSecret)
	if err != nil {
		return err
	}

	// use vpc config to get nodes in this network
	instances, err := getInstancesOf(cfg.VpcID, client)
	if err != nil {
		return err
	}

	if len(instances) == 0 {
		return fmt.Errorf("no instance to be removed (want %v)", nodeCount)
	}

	// filter by post-paid and sort by create time (asc)
	rets, err := filterNodes(client, instances, nodeCount, cfg.DeleteStrategy)
	if err != nil {
		return err
	}

	// remove node labels
	var port int
	if cfg.SSHPort != nil {
		port = *cfg.SSHPort
	} else {
		port = 22
	}
	infos := make([]*NodeInfo, 0)
	for _, ret := range rets {
		infos = append(infos, &NodeInfo{
			InnerIP:  ret.InnerIP,
			HostName: ret.HostName,
		})
	}
	if err := deleteInstancesFromOWCluster(infos, port, "root", cfg.Password, cfg.SSHKeyFile); err != nil {
		return err
	}

	// concat the instance id
	var ids []string
	for _, ret := range rets {
		ids = append(ids, ret.InstanceId)
	}

	// stop the (machine) instances
	for _, id := range ids {
		if _, err := stopInstance(id, client, cfg.Debug); err != nil {
			return (err)
		}
	}

	// delete the (machine) instances
	if len(ids) > 0 {
		if _, err := deleteInstances(&ids, client, cfg.Debug); err != nil {
			return err
		}
	}

	return nil
}

func deleteInstancesFromOWCluster(infos []*NodeInfo, nodeSSHPort int, user string, sshKeyFile, password *string) error {
	if len(infos) == 0 {
		return nil
	}

	var ips, names string
	for idx, info := range infos {
		ips += info.InnerIP
		names += info.HostName
		if idx < len(infos)-1 {
			ips += ","
			names += ","
		}
	}

	if sshKeyFile != nil && len(*sshKeyFile) > 0 {
		// use ssh private key
		_, err := exec.Command("./delete-k8s.sh", "-h", ips, "-P", strconv.Itoa(nodeSSHPort), "-n", names, "-u", user, "-s", *sshKeyFile).Output()
		return err
	}

	// use password
	_, err := exec.Command("./delete-k8s.sh", "-h", ips, "-P", strconv.Itoa(nodeSSHPort), "-n", names, "-u", user, "-p", *password).Output()
	return err
}

func filterNodes(client *ecs.Client, instances []ecs.Instance, filterCount int, deleteStrategy *string) ([]*instancePostChargedCheckResult, error) {
	if len(instances) == 0 {
		return nil, nil
	}

	if deleteStrategy == nil {
		return filterNotSortedNodes(client, instances, filterCount)
	}

	return filterSortedNodes(client, instances, filterCount, *deleteStrategy)
}

func filterNotSortedNodes(client *ecs.Client, instances []ecs.Instance, filterCount int) ([]*instancePostChargedCheckResult, error) {
	rets := make([]*instancePostChargedCheckResult, 0)
	var lock sync.Mutex
	appendValue := func(ret *instancePostChargedCheckResult) {
		lock.Lock()
		rets = append(rets, ret)
		lock.Unlock()
	}
	var wg sync.WaitGroup
	wg.Add(len(instances))
	for idx, instance := range instances {
		go func(idx int, instance ecs.Instance) {
			defer wg.Done()
			ret := checkIfInstancePostCharged(idx, instance.InstanceId, client)
			if ret.Err != nil || !ret.IsPostCharged { // 排除错误和包月/年节点
				return
			}
			appendValue(&ret)
		}(idx, instance)
	}
	wg.Wait()
	// get the front ones
	length := len(rets)
	if filterCount > length {
		filterCount = length
	}
	return rets[:filterCount], nil
}

func filterSortedNodes(client *ecs.Client, instances []ecs.Instance, filterCount int, deleteStrategy string) ([]*instancePostChargedCheckResult, error) {
	rets := sslice.New(false)
	var lock sync.Mutex
	appendValue := func(ret *instancePostChargedCheckResult) {
		lock.Lock()
		rets.Push(ret)
		lock.Unlock()
	}
	var wg sync.WaitGroup
	wg.Add(len(instances))
	for idx, instance := range instances {
		go func(idx int, instance ecs.Instance) {
			defer wg.Done()
			ret := checkIfInstancePostCharged(idx, instance.InstanceId, client)
			if ret.Err != nil || !ret.IsPostCharged { // 排除错误和包月/年节点
				return
			}
			appendValue(&ret)
		}(idx, instance)
	}
	wg.Wait()
	// get the front ones
	length := rets.Len()
	if filterCount > length {
		filterCount = length
	}
	switch deleteStrategy {
	case "oldest":
		// do nothing
	case "latest":
		rets.Reverse()
	default:
		return nil, fmt.Errorf("the delete strategy not supported: %v", deleteStrategy)
	}
	var infos []*instancePostChargedCheckResult
	for idx := 0; idx < filterCount; idx++ {
		infos = append(infos, rets.Get(idx).(*instancePostChargedCheckResult))
	}
	return infos, nil
}

func deleteInstances(ids *[]string, client *ecs.Client, debugMode *bool) (*ecs.DeleteInstancesResponse, error) {
	request := ecs.CreateDeleteInstancesRequest()
	request.InstanceId = ids
	request.ClientToken = fmt.Sprintf("%v", time.Now().Second())
	if debugMode != nil {
		request.DryRun = requests.NewBoolean(*debugMode)
	}
	return client.DeleteInstances(request)
}

func stopInstance(id string, client *ecs.Client, debugMode *bool) (*ecs.StopInstanceResponse, error) {
	request := ecs.CreateStopInstanceRequest()
	request.InstanceId = id
	if debugMode != nil {
		request.DryRun = requests.NewBoolean(*debugMode)
	}
	return client.StopInstance(request)
}

type instancePostChargedCheckResult struct {
	Index         int
	InstanceId    string
	IsPostCharged bool   // YES, 按量付费
	CreateTime    string // 实例创建时间，采用ISO8601表示法，并使用UTC时间，格式为：YYYY-MM-DDThh:mm:ssZ。
	HostName      string
	InnerIP       string
	Err           error
}

func (ipcr *instancePostChargedCheckResult) Compare(other sslice.SortableElement) int {
	thisTime, err := iso8601.Parse([]byte(ipcr.CreateTime))
	if err != nil {
		panic(err)
	}
	otherTime, err := iso8601.Parse([]byte(other.(*instancePostChargedCheckResult).CreateTime))
	if err != nil {
		panic(err)
	}
	if thisTime.Before(otherTime) {
		return -1
	} else if thisTime.After(otherTime) {
		return 1
	} else {
		return 0
	}
}

// check if the instances are post-paid type or not
func checkIfInstancePostCharged(idx int, instanceID string, client *ecs.Client) instancePostChargedCheckResult {
	request := ecs.CreateDescribeInstanceAttributeRequest()
	request.InstanceId = instanceID
	resp, err := client.DescribeInstanceAttribute(request)
	if err != nil {
		return instancePostChargedCheckResult{idx, instanceID, false, "", "", "", err}
	}
	return instancePostChargedCheckResult{idx, instanceID, resp.InstanceChargeType == "PostPaid", resp.CreationTime, resp.HostName, resp.InnerIpAddress.IpAddress[0], nil}
}

func getInstancesOf(vpcID string, client *ecs.Client) (instances []ecs.Instance, err error) {
	pageSize := 100
	page := 1
	for {
		request := ecs.CreateDescribeInstancesRequest()
		request.VpcId = vpcID
		request.PageSize = requests.NewInteger(pageSize)
		request.PageNumber = requests.NewInteger(page)
		resp, err := client.DescribeInstances(request)
		if err != nil {
			return nil, err
		}
		instances = append(instances, resp.Instances.Instance...)
		if len(resp.Instances.Instance) != pageSize { // there are no more instances.
			break
		}
		page++
	}
	return instances, nil
}
