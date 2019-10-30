package main

import (
	"fmt"
	"os"
	"sync"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
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
	RegionId     string `yaml:"region-id"`
	AccessId     string `yaml:"access-key-id"`
	AccessSecret string `yaml:"access-key-secret"`
	VPCId        string `yaml:"vpc-id"`
	NodeCount    *int   `yaml:"node-count,omitempty"`
	Debug        *bool  `yaml:"debug,omitempty"`
}

func startClient(ctx *cli.Context) error {
	var regionID, accessKey, accessSecret string
	var vpcID string
	var nodeCount int
	debugMode := false
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

		if cfg.Debug != nil {
			debugMode = *cfg.Debug
		}
	} else {
		regionID = ctx.String(regionIDLong)
		accessKey = ctx.String(accessKeyIDLong)
		accessSecret = ctx.String(accessKeySecretLong)
		vpcID = ctx.String(vpcIDLong)
		nodeCount = ctx.Int(nodeCountLong)
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

	// 筛选节点未按量付费版本并按创建时间排序
	var instanceRets = make([]<-chan instancePostChargedCheckResult, len(instances))
	var instancePostCharges = make([]bool, len(instances))
	var wg sync.WaitGroup
	wg.Add(len(instances))
	for _, instance := range instances {
		ret := checkIfInstancePostCharged(instance.InstanceId, client, &wg)
		instanceRets = append(instanceRets, ret)
	}
	wg.Wait()
	for idx, ret := range instanceRets {
		for {
			select {
			case result := <-ret:
				instancePostCharges[idx] = result.IsPostCharged
			default:
				fmt.Println("collect done, go back")
				break
			}
		}
	}
	//filter and sort

	// results := make([]<-chan instancePostChargedCheckResult, len(instances))
	// for _, instance := range instances {
	// 	result := checkIfInstancePostCharged(instance.InstanceId, client)
	// 	results = append(results, result)
	// }
	// merge(func(c <-chan instancePostChargedCheckResult, wg *sync.WaitGroup) {
	// 	wg.Done() //减少一个goroutine
	// }, results...)

	// 停止N个节点
	mode := "release"
	if debugMode {
		mode = "debug"
	}
	fmt.Printf("we need to stop %v nodes, in %v mode", nodeCount, mode)

	// 删除N个节点

	return nil
}

type instancePostChargedCheckResult struct {
	IsPostCharged bool // YES, 按量付费
	Err           error
}

// 查看实例是否为按量付费
func checkIfInstancePostCharged(instanceID string, client *ecs.Client, wg *sync.WaitGroup) <-chan instancePostChargedCheckResult {
	out := make(chan instancePostChargedCheckResult)
	go func() {
		defer close(out)
		defer (*wg).Done()
		request := ecs.CreateDescribeInstanceAttributeRequest()
		request.InstanceId = instanceID
		resp, err := client.DescribeInstanceAttribute(request)
		if err != nil {
			out <- instancePostChargedCheckResult{false, err}
			return
		}
		out <- instancePostChargedCheckResult{resp.InstanceChargeType == "PostPaid", nil}
	}()
	return out
}

func checkIfInstancePostChargedByChan(instanceID string, client *ecs.Client) <-chan instancePostChargedCheckResult {
	out := make(chan instancePostChargedCheckResult)
	go func() {
		defer close(out)
		request := ecs.CreateDescribeInstanceAttributeRequest()
		request.InstanceId = instanceID
		resp, err := client.DescribeInstanceAttribute(request)
		if err != nil {
			out <- instancePostChargedCheckResult{false, err}
			return
		}
		out <- instancePostChargedCheckResult{resp.InstanceChargeType == "PostPaid", nil}
	}()
	return out
}

func merge(output func(c <-chan instancePostChargedCheckResult, wg *sync.WaitGroup), cs ...<-chan instancePostChargedCheckResult) <-chan error {
	var wg sync.WaitGroup
	out := make(chan error)

	wg.Add(len(cs)) //要执行的goroutine个数
	for _, c := range cs {
		go output(c, &wg) //对传入的多个channel执行output
	}

	// Start a goroutine to close out once all the output goroutines are
	// done.  This must start after the wg.Add call.
	go func() {
		wg.Wait()  //等待，直到所有goroutine都完成后
		close(out) //所有的都放到out后关闭
	}()
	return out
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
