package main

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/relvacode/iso8601"
	"github.com/yaa110/sslice"
	cli "gopkg.in/urfave/cli.v1"
)

const (
	regionIDLong         = "region-id"
	regionIDShort        = "r"
	accessKeyIDLong      = "access-key-id"
	accessKeyIDShort     = "k"
	accessKeySecretLong  = "access-key-secret"
	accessKeySecretShort = "s"
	configFileLong       = "config"
	configFileShort      = "c"
	vpcIDLong            = "vpc-id"
	vpcIDShort           = "p"
	nodeCountLong        = "node-count"
	nodeCountShort       = "n"
	deleteStrategyLong   = "delete-strategy"
	deleteStrategyShort  = "d"
	debugKeyLong         = "debug"
)

func main() {
	app := cli.NewApp()

	app.Name = "AliyunECSDeleter"
	app.Version = "0.0.1"
	app.Description = "fdn aliyun 资源移除工具"
	app.Authors = []cli.Author{
		{Name: "FDN developper"},
	}

	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "config,c",
			Usage: "配置文件路径，使用后必选参数使用该文件内容，可选参数优先使用该文件内容",
		},
		cli.StringFlag{
			Name:  "region-id,r",
			Usage: "区域编号，要求一定要有",
		},
		cli.StringFlag{
			Name:  "access-key-id,k",
			Usage: "授权KeyID",
		},
		cli.StringFlag{
			Name:  "access-key-secret,s",
			Usage: "授权Key秘钥",
		},
		cli.StringFlag{
			Name:  "vpc-id,p",
			Usage: "专用网络ID",
		},
		cli.IntFlag{
			Name:  "node-count,n",
			Usage: "移除节点数量",
			Value: 1,
		},
		cli.StringFlag{
			Name:  "delete-strategy,d",
			Usage: "移除策略，支持\"oldest\"/\"latest\"，即最旧/最新优先原则",
			Value: "oldest",
		},
		cli.BoolFlag{
			Name:  debugKeyLong,
			Usage: "调试用，不直接运行",
		},
	}
	app.Action = startClient

	err := app.Run(os.Args)
	if err != nil {
		panic(err)
	}
}

type EcsConfig struct {
	RegionId     string  `yaml:"region-id"`
	AccessId     string  `yaml:"access-key-id"`
	AccessSecret string  `yaml:"access-key-secret"`
	VPCId        string  `yaml:"vpc-id"`
	NodeCount    *int    `yaml:"node-count,omitempty"`
	StrategyMode *string `yaml:"strategy-mode,omitempty"`
	Debug        *bool   `yaml:"debug,omitempty"`
}

func startClient(ctx *cli.Context) error {
	var regionID, accessKey, accessSecret string
	var vpcID string
	var nodeCount int
	debugMode := false
	deleteStrategy := "oldest"

	if path := ctx.String(configFileLong); len(path) > 0 {
		var cfg EcsConfig
		if err := ReadYamlFile(path, &cfg); err != nil {
			return err
		}
		regionID = cfg.RegionId
		accessKey = cfg.AccessId
		accessSecret = cfg.AccessSecret
		vpcID = cfg.VPCId

		// optional args for default value
		if cfg.NodeCount != nil {
			nodeCount = *cfg.NodeCount
		} else {
			nodeCount = ctx.Int(nodeCountLong)
		}
		if cfg.StrategyMode != nil {
			deleteStrategy = *cfg.StrategyMode
		} else {
			deleteStrategy = ctx.String(deleteStrategyLong)
		}

		if cfg.Debug != nil {
			debugMode = *cfg.Debug
		}
	} else {
		regionID = ctx.String(regionIDLong)
		accessKey = ctx.String(accessKeyIDLong)
		accessSecret = ctx.String(accessKeySecretLong)
		vpcID = ctx.String(vpcIDLong)
		nodeCount = ctx.Int(nodeCountLong)
		deleteStrategy = ctx.String(deleteStrategyLong)
		debugMode = ctx.Bool(debugKeyLong)
	}

	client, err := ecs.NewClientWithAccessKey(regionID, accessKey, accessSecret)
	if err != nil {
		return err
	}

	// 根据VPC配置读取节点信息
	instances, err := getInstancesOf(vpcID, client)
	if err != nil {
		return err
	}

	if len(instances) == 0 {
		return fmt.Errorf("no instance to be removed (want %v)", nodeCount)
	}

	// 筛选节点未按量付费版本并按创建时间排序
	rets, err := filterNodes(client, instances, nodeCount, deleteStrategy)
	if err != nil {
		return err
	}

	// 测试用，记得删除
	// rets = []*instancePostChargedCheckResult{
	// 	&instancePostChargedCheckResult{
	// 		HostName: "wjlfw02",
	// 	},
	// }

	// 移除节点标签
	if err := deleteInstancesFromOWCluster(rets); err != nil {
		return err
	}

	// 拼接ID
	var ids []string
	for _, ret := range rets {
		ids = append(ids, ret.InstanceId)
	}

	// 停止N个节点
	for _, id := range ids {
		if _, err := stopInstance(id, client, debugMode); err != nil {
			return (err)
		}
	}

	// 删除N个节点
	if len(ids) > 0 {
		if _, err := deleteInstances(&ids, client, debugMode); err != nil {
			return err
		}
	}

	return nil
}

func deleteInstancesFromOWCluster(infos []*instancePostChargedCheckResult) error {
	if len(infos) == 0 {
		return nil
	}

	var names string
	for idx, info := range infos {
		names += info.HostName
		if idx < len(infos)-1 {
			names += ","
		}
	}

	_, err := exec.Command("./delete-k8s.sh", "-n", names).Output()
	return err
}

func filterNodes(client *ecs.Client, instances []ecs.Instance, filterCount int, deleteStrategy string) ([]*instancePostChargedCheckResult, error) {
	if len(instances) == 0 {
		return nil, nil
	}

	rets := sslice.New(false)
	var lock sync.Mutex //互斥锁
	appendValue := func(ret *instancePostChargedCheckResult) {
		lock.Lock() //加锁
		rets.Push(ret)
		lock.Unlock() //解锁
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
	// 取出前几个
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

func deleteInstances(ids *[]string, client *ecs.Client, debugMode bool) (*ecs.DeleteInstancesResponse, error) {
	request := ecs.CreateDeleteInstancesRequest()
	request.InstanceId = ids
	request.ClientToken = fmt.Sprintf("%v", time.Now().Second())
	request.DryRun = requests.NewBoolean(debugMode)
	return client.DeleteInstances(request)
}

func stopInstance(id string, client *ecs.Client, debugMode bool) (*ecs.StopInstanceResponse, error) {
	request := ecs.CreateStopInstanceRequest()
	request.InstanceId = id
	request.DryRun = requests.NewBoolean(debugMode)
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

// 查看实例是否为按量付费
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
