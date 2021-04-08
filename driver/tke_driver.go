package driver

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rancher/kontainer-engine/drivers/options"
	"github.com/rancher/kontainer-engine/drivers/util"
	"github.com/rancher/kontainer-engine/types"
	"github.com/rancher/rke/log"
	"github.com/sirupsen/logrus"
	tccommon "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	cvm "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cvm/v20170312"
	tke "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/tke/v20180525"
	"golang.org/x/net/context"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	runningStatus  = "Running"
	successStatus  = "Created"
	failedStatus   = "CreateFailed"
	notReadyStatus = "ClusterNotReadyError"
	retries        = 5
	pollInterval   = 30
)

var (
	instanceDeleteModeTerminate = "terminate"
	managedClusterType          = "MANAGED_CLUSTER"
)

// Driver defines the struct of tke driver
type Driver struct {
	driverCapabilities types.Capabilities
}
type state struct {
	// The id of the cluster
	ClusterID string
	// The name of the cluster
	ClusterName string
	// The description of the cluster
	ClusterDesc string
	// CIDR used to assign cluster containers and service IPs must not conflict with VPC CIDR or with other cluster CIDRs in the same VPC (*required)
	ClusterCIDR string
	// Whether to ignore the ClusterCIDR conflict error, the default is 0
	// 0: Do not ignore the conflict (and return an error); 1: Ignore the conflict (continue to create)
	IgnoreClusterCIDRConflict int64
	// The version of the cluster
	ClusterVersion string
	// Create a empty cluster
	EmptyCluster bool
	// The region of the cluster
	Region string
	// The secret id used for authentication
	SecretID string
	// The secret key used for authentication
	SecretKey string
	// cluster state
	State string
	// The project ID of the cluster
	ProjectID int64

	// The zone id of the cluster
	ZoneID string
	// The image id of the node
	ImageID string
	// The number of nodes purchased, up to 100
	GoodsNum int64
	// CPU core number
	CPU int64
	// Memory size (GB)
	Mem int64
	// System name, Centos7.2x86_64 or ubuntu16.04.1 LTSx86_64, all nodes in the cluster use this system,
	// the extension node will also automatically use this system (*required)
	OsName string
	// See CVM Instance Configuration for details . Default: S1.SMALL1
	InstanceType string
	// The type of node, the default is PayByHour
	// another option is PayByMonth
	CvmType string
	// The annual renewal fee for the annual subscription, default to NOTIFY_AND_AUTO_RENEW
	RenewFlag string
	// Type of bandwidth
	// PayByMonth vm: PayByMonth, PayByTraffic,
	// PayByHour vm: PayByHour, PayByTraffic
	BandwidthType string
	// Public network bandwidth (Mbps), when the traffic is charged for the public network bandwidth peak
	Bandwidth int64
	// Whether to open the public network IP, 0: not open 1: open
	WanIP int64
	// Private network ID
	VpcID string
	// Subnet ID
	SubnetID string
	// Whether it is a public network gateway
	// 0: non-public network gateway
	// 1: public network gateway
	IsVpcGateway int64
	// system disk size. linux system adjustment range is 20 - 50g, step size is 1
	RootSize int64
	// System disk type. System disk type restrictions are detailed in the CVM instance configuration.
	// default value of the SSD cloud drive : CLOUD_BASIC.
	RootType string
	// Data disk size (GB)
	StorageSize int64
	// Data disk type
	StorageType string
	// Node password
	Password string
	// Key ID
	KeyID string
	// The annual subscription period of the annual subscription month, unit month. This parameter is required when cvmType is PayByMonth
	Period int64
	// Security group ID, default does not bind any security groups, please fill out the inquiry list of security groups sgId field interface returned
	SgID string
	// The cluster master occupies the IP of a VPC subnet. This parameter specifies which subnet the IP is occupied by the master.
	// This subnet must be in the same VPC as the cluster.
	MasterSubnetID string
	// Base64-encoded user script, which is executed after the k8s component is run. The user is required to guarantee the reentrant and retry logic of the script.
	// The script and its generated log file can be viewed in the /data/ccs_userscript/ path of the node.
	UserScript string

	// cluster info
	ClusterInfo types.ClusterInfo
	// The total number of worker node
	NodeCount int64
}

// NewDriver init the TKE driver
func NewDriver() types.Driver {
	logrus.Println("init new driver")
	driver := &Driver{
		driverCapabilities: types.Capabilities{
			Capabilities: make(map[int64]bool),
		},
	}

	driver.driverCapabilities.AddCapability(types.GetVersionCapability)
	driver.driverCapabilities.AddCapability(types.SetVersionCapability)
	driver.driverCapabilities.AddCapability(types.GetClusterSizeCapability)
	driver.driverCapabilities.AddCapability(types.SetClusterSizeCapability)
	return driver
}

// GetDriverCreateOptions implements driver interface
func (d *Driver) GetDriverCreateOptions(ctx context.Context) (*types.DriverFlags, error) {
	driverFlag := types.DriverFlags{
		Options: make(map[string]*types.Flag),
	}
	driverFlag.Options["name"] = &types.Flag{
		Type:  types.StringType,
		Usage: "the internal name of the cluster in Rancher",
	}
	driverFlag.Options["secret-id"] = &types.Flag{
		Type:  types.StringType,
		Usage: "The secretID",
	}
	driverFlag.Options["secret-key"] = &types.Flag{
		Type:     types.StringType,
		Password: true,
		Usage:    "The version of the cluster",
	}
	driverFlag.Options["cluster-name"] = &types.Flag{
		Type:  types.StringType,
		Usage: "The name of the cluster that should be displayed to the user",
	}
	driverFlag.Options["cluster-desc"] = &types.Flag{
		Type:  types.StringType,
		Usage: "The description of the cluster",
	}
	driverFlag.Options["cluster-cidr"] = &types.Flag{
		Type:  types.StringType,
		Usage: "The IP address range of the container pods, must not conflict with VPC CIDR",
	}
	driverFlag.Options["ignore-cluster-cidr-conflict"] = &types.Flag{
		Type:    types.IntType,
		Usage:   "Whether to ignore the ClusterCIDR conflict error, the default is 0",
		Default: &types.Default{DefaultInt: 0},
	}
	driverFlag.Options["cluster-version"] = &types.Flag{
		Type:    types.StringType,
		Usage:   "The version of the cluster",
		Default: &types.Default{DefaultString: "1.10.5"},
	}
	driverFlag.Options["empty-cluster"] = &types.Flag{
		Type:    types.BoolType,
		Usage:   "Create a empty cluster",
		Default: &types.Default{DefaultBool: false},
	}
	driverFlag.Options["region"] = &types.Flag{
		Type:  types.StringType,
		Usage: "The region of the cluster",
	}
	driverFlag.Options["project-id"] = &types.Flag{
		Type:  types.IntType,
		Usage: "The ID of your project to use when creating a cluster",
	}
	driverFlag.Options["zoneId"] = &types.Flag{
		Type:  types.StringType,
		Usage: "The zone id of the cluster",
	}
	driverFlag.Options["imageId"] = &types.Flag{
		Type:    types.StringType,
		Usage:   "The image id of the node,  default value img-pi0ii46r (ubuntu Server 18.04.1 LTS 64)",
		Default: &types.Default{DefaultString: "img-pi0ii46r"},
	}
	driverFlag.Options["node-count"] = &types.Flag{
		Type:  types.IntType,
		Usage: "The node count of the cluster, up to 100",
	}
	driverFlag.Options["cpu"] = &types.Flag{
		Type:  types.IntType,
		Usage: "Cpu core number",
	}
	driverFlag.Options["mem"] = &types.Flag{
		Type:  types.IntType,
		Usage: "Memory size (GB)",
	}
	driverFlag.Options["os-name"] = &types.Flag{
		Type:    types.StringType,
		Usage:   "The name of the operating system , currently supports Centos7.2x86_64 or ubuntu16.04.1 LTSx86_64",
		Default: &types.Default{DefaultString: "ubuntu16.04.1 LTSx86_64"},
	}
	driverFlag.Options["instance-type"] = &types.Flag{
		Type:    types.StringType,
		Usage:   "See CVM Instance Configuration for details . Default: S2.MEDIUM4",
		Default: &types.Default{DefaultString: "S2.MEDIUM4"},
	}
	driverFlag.Options["cvm-type"] = &types.Flag{
		Type:    types.StringType,
		Usage:   "The cvm type of node, default to POSTPAID_BY_HOUR",
		Default: &types.Default{DefaultString: "POSTPAID_BY_HOUR"},
	}
	driverFlag.Options["renew-flag"] = &types.Flag{
		Type:  types.StringType,
		Usage: "The annual renewal fee for the annual subscription, default to NOTIFY_AND_AUTO_RENEW",
	}
	driverFlag.Options["bandwidth-type"] = &types.Flag{
		Type:    types.StringType,
		Usage:   "Type of bandwidth",
		Default: &types.Default{DefaultString: "TRAFFIC_POSTPAID_BY_HOUR"},
	}
	driverFlag.Options["bandwidth"] = &types.Flag{
		Type:    types.IntType,
		Usage:   "Public network bandwidth (Mbps), when the traffic is charged for the public network bandwidth peak",
		Default: &types.Default{DefaultInt: 10},
	}
	driverFlag.Options["wan-ip"] = &types.Flag{
		Type:    types.IntType,
		Usage:   "the cluster master occupies the IP of a VPC subnet",
		Default: &types.Default{DefaultInt: 1},
	}
	driverFlag.Options["vpc-id"] = &types.Flag{
		Type:  types.StringType,
		Usage: "Private network ID, please fill out the inquiry list private network interface returned unVpcId (private network unified ID) field",
	}
	driverFlag.Options["subnet-id"] = &types.Flag{
		Type:  types.StringType,
		Usage: "Subnet ID, please fill out the inquiry list of subnets interface returned unSubnetId (unified subnet ID) field",
	}
	driverFlag.Options["is-vpc-gateway"] = &types.Flag{
		Type:    types.IntType,
		Usage:   "Whether it is a public network gateway, network gateway only in public with a public IP, and in order to work properly when under private network",
		Default: &types.Default{DefaultInt: 0},
	}
	driverFlag.Options["root-size"] = &types.Flag{
		Type:    types.IntType,
		Usage:   "System disk size. Linux system adjustment range is 20 - 50G, step size is 1",
		Default: &types.Default{DefaultInt: 25},
	}
	driverFlag.Options["root-type"] = &types.Flag{
		Type:    types.StringType,
		Usage:   "System disk type. System disk type restrictions are detailed in the CVM instance configuration",
		Default: &types.Default{DefaultString: "CLOUD_BASIC"},
	}
	driverFlag.Options["storage-size"] = &types.Flag{
		Type:    types.IntType,
		Usage:   "Data disk size (GB), the step size is 10",
		Default: &types.Default{DefaultInt: 20},
	}
	driverFlag.Options["storage-type"] = &types.Flag{
		Type:    types.StringType,
		Usage:   "Data disk type, default value of the SSD cloud drive",
		Default: &types.Default{DefaultString: "CLOUD_BASIC"},
	}
	driverFlag.Options["password"] = &types.Flag{
		Type:  types.StringType,
		Usage: "Node password. If it is not set, it will be randomly generated and sent by the station letter",
	}
	driverFlag.Options["key-id"] = &types.Flag{
		Type:  types.StringType,
		Usage: "Key id, after associating the key can be used to logging to the node",
	}
	driverFlag.Options["period"] = &types.Flag{
		Type:  types.IntType,
		Usage: "The annual subscription period of the annual subscription month, unit month. This parameter is required when cvmType is PayByMonth",
	}
	driverFlag.Options["sg-id"] = &types.Flag{
		Type:  types.StringType,
		Usage: "Security group ID, default does not bind any security groups",
	}
	driverFlag.Options["master-subnet-id"] = &types.Flag{
		Type:  types.StringType,
		Usage: "the cluster master occupies the IP of a VPC subnet",
	}
	driverFlag.Options["user-script"] = &types.Flag{
		Type:  types.StringType,
		Usage: "Base64-encoded user script, which is executed after the k8s component is run.",
	}
	return &driverFlag, nil
}

// GetDriverUpdateOptions implements driver interface
func (d *Driver) GetDriverUpdateOptions(ctx context.Context) (*types.DriverFlags, error) {
	driverFlag := types.DriverFlags{
		Options: make(map[string]*types.Flag),
	}
	driverFlag.Options["secret-id"] = &types.Flag{
		Type:  types.StringType,
		Usage: "The secretID of the cluster",
	}
	driverFlag.Options["secret-key"] = &types.Flag{
		Type:     types.StringType,
		Password: true,
		Usage:    "The secretKey of the cluster",
	}
	driverFlag.Options["node-count"] = &types.Flag{
		Type:  types.IntType,
		Usage: "The number of nodes purchased, up to 100",
	}
	driverFlag.Options["instance-type"] = &types.Flag{
		Type:  types.StringType,
		Usage: "See CVM Instance Configuration for details . Default: S2.MEDIUM4",
	}
	driverFlag.Options["storage-size"] = &types.Flag{
		Type:  types.IntType,
		Usage: "Data disk size (GB), the step size is 10",
	}
	driverFlag.Options["root-size"] = &types.Flag{
		Type:  types.IntType,
		Usage: "System disk size. Linux system adjustment range is 20 - 50G, step size is 1",
	}
	driverFlag.Options["cluster-name"] = &types.Flag{
		Type:  types.StringType,
		Usage: "The name of the cluster that should be displayed to the user",
	}
	driverFlag.Options["cluster-desc"] = &types.Flag{
		Type:  types.StringType,
		Usage: "The description of the cluster",
	}
	driverFlag.Options["project-id"] = &types.Flag{
		Type:  types.IntType,
		Usage: "The ID of your project to use when creating a cluster",
	}
	driverFlag.Options["bandwidth-type"] = &types.Flag{
		Type:  types.StringType,
		Usage: "Type of bandwidth, PayByTraffic or PayByHour",
	}
	driverFlag.Options["bandwidth"] = &types.Flag{
		Type:  types.IntType,
		Usage: "Public network bandwidth (Mbps), when the traffic is charged for the public network bandwidth peak",
	}
	driverFlag.Options["wan-ip"] = &types.Flag{
		Type:  types.IntType,
		Usage: "the cluster master occupies the IP of a VPC subnet",
	}
	driverFlag.Options["is-vpc-gateway"] = &types.Flag{
		Type:  types.IntType,
		Usage: "Whether it is a public network gateway, network gateway only in public with a public IP, and in order to work properly when under private network",
	}
	return &driverFlag, nil
}

// getStateFromOpts gets input values from opts
func getStateFromOpts(driverOptions *types.DriverOptions, isCreate bool) (*state, error) {
	d := &state{
		ClusterInfo: types.ClusterInfo{
			Metadata: map[string]string{},
		},
	}
	d.ClusterName = options.GetValueFromDriverOptions(driverOptions, types.StringType, "cluster-name", "clusterName").(string)
	d.ClusterDesc = options.GetValueFromDriverOptions(driverOptions, types.StringType, "cluster-desc", "clusterDesc").(string)
	d.ClusterCIDR = options.GetValueFromDriverOptions(driverOptions, types.StringType, "cluster-cidr", "clusterCidr").(string)
	d.IgnoreClusterCIDRConflict = options.GetValueFromDriverOptions(driverOptions, types.IntType, "ingore-cluster-cidr-conflict", "ignoreClusterCidrConflict").(int64)
	d.ClusterVersion = options.GetValueFromDriverOptions(driverOptions, types.StringType, "cluster-version", "clusterVersion").(string)
	d.EmptyCluster = options.GetValueFromDriverOptions(driverOptions, types.BoolType, "empty-cluster", "emptyCluster").(bool)
	d.Region = options.GetValueFromDriverOptions(driverOptions, types.StringType, "region").(string)
	d.SecretID = options.GetValueFromDriverOptions(driverOptions, types.StringType, "secret-id", "secretId").(string)
	d.SecretKey = options.GetValueFromDriverOptions(driverOptions, types.StringType, "secret-key", "secretKey").(string)
	d.ProjectID = options.GetValueFromDriverOptions(driverOptions, types.IntType, "project-id", "projectId").(int64)

	d.ZoneID = options.GetValueFromDriverOptions(driverOptions, types.StringType, "zone-id", "zoneId").(string)
	d.ImageID = options.GetValueFromDriverOptions(driverOptions, types.StringType, "image-id", "imageId").(string)
	d.CPU = options.GetValueFromDriverOptions(driverOptions, types.IntType, "cpu").(int64)
	d.Mem = options.GetValueFromDriverOptions(driverOptions, types.IntType, "mem").(int64)
	d.OsName = options.GetValueFromDriverOptions(driverOptions, types.StringType, "os-name", "osName").(string)
	d.InstanceType = options.GetValueFromDriverOptions(driverOptions, types.StringType, "instance-type", "instanceType").(string)
	d.CvmType = options.GetValueFromDriverOptions(driverOptions, types.StringType, "cvm-type", "cvmType").(string)
	d.RenewFlag = options.GetValueFromDriverOptions(driverOptions, types.StringType, "renew-flag", "renewFlag").(string)
	d.BandwidthType = options.GetValueFromDriverOptions(driverOptions, types.StringType, "bandwidth-type", "bandwidthType").(string)
	d.Bandwidth = options.GetValueFromDriverOptions(driverOptions, types.IntType, "bandwidth", "bandwidth").(int64)
	d.WanIP = options.GetValueFromDriverOptions(driverOptions, types.IntType, "wan-ip", "wanIp").(int64)
	d.VpcID = options.GetValueFromDriverOptions(driverOptions, types.StringType, "vpc-id", "vpcId").(string)
	d.SubnetID = options.GetValueFromDriverOptions(driverOptions, types.StringType, "subnet-id", "subnetId").(string)
	d.IsVpcGateway = options.GetValueFromDriverOptions(driverOptions, types.IntType, "is-vpc-gateway", "isVpcGateway").(int64)
	d.RootSize = options.GetValueFromDriverOptions(driverOptions, types.IntType, "root-size", "rootSize").(int64)
	d.RootType = options.GetValueFromDriverOptions(driverOptions, types.StringType, "root-type", "rootType").(string)
	d.StorageSize = options.GetValueFromDriverOptions(driverOptions, types.IntType, "storage-size", "storageSize").(int64)
	d.StorageType = options.GetValueFromDriverOptions(driverOptions, types.StringType, "storage-type", "storageType").(string)
	d.Password = options.GetValueFromDriverOptions(driverOptions, types.StringType, "password").(string)
	d.KeyID = options.GetValueFromDriverOptions(driverOptions, types.StringType, "key-id", "keyId").(string)
	d.Period = options.GetValueFromDriverOptions(driverOptions, types.IntType, "period").(int64)
	d.SgID = options.GetValueFromDriverOptions(driverOptions, types.StringType, "sg-id", "sgId").(string)
	d.MasterSubnetID = options.GetValueFromDriverOptions(driverOptions, types.StringType, "master-subnet-id", "masterSubnetId").(string)
	d.UserScript = options.GetValueFromDriverOptions(driverOptions, types.StringType, "user-script", "userScript").(string)
	d.NodeCount = options.GetValueFromDriverOptions(driverOptions, types.IntType, "node-count", "nodeCount").(int64)

	return d, d.validate(isCreate)
}

func (s *state) validate(isCreate bool) error {
	if isCreate {
		if s.ClusterName == "" {
			return fmt.Errorf("cluster name is required")
		} else if s.ClusterVersion == "" {
			return fmt.Errorf("cluster version is required")
		} else if s.Region == "" {
			return fmt.Errorf("cluster region is required")
		} else if s.SubnetID == "" {
			return fmt.Errorf("cluster subnetID is required")
		} else if s.ZoneID == "" {
			return fmt.Errorf("cluster zoneID is required")
		} else if s.VpcID == "" {
			return fmt.Errorf("cluster vpcID is required")
		} else if s.RootSize == 0 {
			return fmt.Errorf("rootSize should not be set to 0")
		} else if s.StorageSize == 0 {
			return fmt.Errorf("storageSize should not be set to 0")
		} else if s.ClusterCIDR == "" {
			return fmt.Errorf("cluster cidr is required")
		}
	}

	if s.SecretID == "" {
		return fmt.Errorf("secretID is required")
	} else if s.SecretKey == "" {
		return fmt.Errorf("secretKey is required")
	}
	return nil
}

func getTKEServiceClient(state *state, method string) (*tke.Client, error) {
	credential := tccommon.NewCredential(state.SecretID, state.SecretKey)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.ReqTimeout = 20
	cpf.SignMethod = "HmacSHA1"
	cpf.HttpProfile.ReqMethod = method

	client, err := tke.NewClient(credential, state.Region, cpf)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func getCVMServiceClient(state *state, method string) (*cvm.Client, error) {
	credential := tccommon.NewCredential(state.SecretID, state.SecretKey)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.ReqTimeout = 20
	cpf.SignMethod = "HmacSHA1"
	cpf.HttpProfile.ReqMethod = method

	client, err := cvm.NewClient(credential, state.Region, cpf)
	if err != nil {
		return nil, err
	}

	return client, nil
}

// Create implements driver create interface
func (d *Driver) Create(ctx context.Context, opts *types.DriverOptions, _ *types.ClusterInfo) (*types.ClusterInfo, error) {
	state, err := getStateFromOpts(opts, true)
	if err != nil {
		return nil, err
	}

	// init tke service client
	svc, err := getTKEServiceClient(state, "POST")
	if err != nil {
		return nil, err
	}

	req, err := d.getWrapCreateClusterRequest(state)
	if err != nil {
		return nil, err
	}

	info := &types.ClusterInfo{}
	defer storeState(info, state)

	// init tke client and make create cluster api request
	resp, err := svc.CreateCluster(req)
	if err != nil {
		return info, err
	}

	state.ClusterID = *resp.Response.ClusterId
	logrus.Debugf("Cluster %s create is called for region %s and zone %s.", state.ClusterID, state.Region, state.ZoneID)

	if err := waitTKECluster(ctx, svc, state); err != nil {
		return info, err
	}

	return info, nil
}

func (d *Driver) getWrapCreateClusterRequest(state *state) (*tke.CreateClusterRequest, error) {
	logrus.Info("invoking createCluster")
	state.GoodsNum = state.NodeCount

	stringRunInstancesPara, err := getRunInstancesPara(state)
	if err != nil {
		return nil, err
	}

	request := tke.NewCreateClusterRequest()

	request.ClusterCIDRSettings = &tke.ClusterCIDRSettings{
		ClusterCIDR:               tccommon.StringPtr(state.ClusterCIDR),
		IgnoreClusterCIDRConflict: tccommon.BoolPtr(getBoolean(state.IgnoreClusterCIDRConflict)),
	}
	request.ClusterType = tccommon.StringPtr(managedClusterType)

	request.RunInstancesForNode = []*tke.RunInstancesForNode{
		{
			NodeRole: tccommon.StringPtr("WORKER"),
			RunInstancesPara: []*string{
				tccommon.StringPtr(stringRunInstancesPara),
			},
		},
	}

	request.ClusterBasicSettings = &tke.ClusterBasicSettings{
		ClusterOs:          tccommon.StringPtr(state.OsName),
		ClusterVersion:     tccommon.StringPtr(state.ClusterVersion),
		ClusterName:        tccommon.StringPtr(state.ClusterName),
		ClusterDescription: tccommon.StringPtr(state.ClusterDesc),
		VpcId:              tccommon.StringPtr(state.VpcID),
		ProjectId:          tccommon.Int64Ptr(state.ProjectID),
	}

	return request, nil
}

func getRunInstancesPara(state *state) (string, error) {
	zone, err := getZone(state)
	if err != nil {
		return "", err
	}

	runInstancesPara := cvm.NewRunInstancesRequest()
	runInstancesPara.ImageId = tccommon.StringPtr(state.ImageID)
	runInstancesPara.Placement = &cvm.Placement{
		Zone: tccommon.StringPtr(zone),
	}
	runInstancesPara.InstanceChargeType = tccommon.StringPtr(getInstanceChargeType(state.CvmType))
	runInstancesPara.InstanceCount = tccommon.Int64Ptr(state.GoodsNum)
	runInstancesPara.InstanceType = tccommon.StringPtr(state.InstanceType)
	runInstancesPara.InternetAccessible = &cvm.InternetAccessible{
		InternetChargeType:      tccommon.StringPtr(getInternetChargeType(state.BandwidthType)),
		InternetMaxBandwidthOut: tccommon.Int64Ptr(state.Bandwidth),
		PublicIpAssigned:        tccommon.BoolPtr(getBoolean(state.WanIP)),
	}

	runInstancesPara.VirtualPrivateCloud = &cvm.VirtualPrivateCloud{
		SubnetId:     tccommon.StringPtr(state.SubnetID),
		VpcId:        tccommon.StringPtr(state.VpcID),
		AsVpcGateway: tccommon.BoolPtr(getBoolean(state.IsVpcGateway)),
	}

	runInstancesPara.SystemDisk = &cvm.SystemDisk{
		DiskType: tccommon.StringPtr(state.RootType),
		DiskSize: tccommon.Int64Ptr(state.RootSize),
	}

	runInstancesPara.DataDisks = []*cvm.DataDisk{
		{
			DiskType: tccommon.StringPtr(state.StorageType),
			DiskSize: tccommon.Int64Ptr(state.StorageSize),
		},
	}

	runInstancesPara.LoginSettings = &cvm.LoginSettings{
		KeyIds: []*string{
			tccommon.StringPtr(state.KeyID),
		},
	}
	runInstancesPara.SecurityGroupIds = []*string{
		tccommon.StringPtr(state.SgID),
	}
	stringRunInstancesPara := runInstancesPara.ToJsonString()

	return stringRunInstancesPara, nil
}

func waitTKECluster(ctx context.Context, svc *tke.Client, state *state) error {
	lastMsg := ""
	timeout := time.Duration(30 * time.Minute)
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	tick := TickerContext(timeoutCtx, 15*time.Second)
	defer cancel()

	// Keep trying until we're timed out or got a result or got an error
	for {
		select {
		// Got a timeout! fail with a timeout error
		case <-timeoutCtx.Done():
			return fmt.Errorf("timed out waiting cluster %s to be ready", state.ClusterName)
		// Got a tick, check cluster provisioning status
		case <-tick:
			cluster, err := getCluster(svc, state)
			if err != nil && !strings.Contains(err.Error(), notReadyStatus) {
				return err
			}

			if *cluster.Response.Clusters[0].ClusterStatus != lastMsg {
				log.Infof(ctx, "provisioning cluster %s: %s", state.ClusterName, *cluster.Response.Clusters[0].ClusterStatus)
				lastMsg = *cluster.Response.Clusters[0].ClusterStatus
			}

			if *cluster.Response.Clusters[0].ClusterStatus == runningStatus {
				log.Infof(ctx, "cluster %v is running", state.ClusterName)
				return nil
			} else if *cluster.Response.Clusters[0].ClusterStatus == failedStatus {
				return fmt.Errorf("tencent cloud failed to provision cluster")
			}
		}
	}
}

func getCluster(svc *tke.Client, state *state) (*tke.DescribeClustersResponse, error) {
	logrus.Infof("invoking getCluster")
	req, err := getWrapDescribeClusterRequest(state)
	if err != nil {
		return nil, err
	}

	resp, err := svc.DescribeClusters(req)
	if err != nil {
		return resp, fmt.Errorf("an API error has returned: %s", err)
	}

	if *resp.Response.TotalCount <= 0 {
		return nil, fmt.Errorf("cluster %s is not found", state.ClusterName)
	}
	return resp, nil
}

func getWrapDescribeClusterRequest(state *state) (*tke.DescribeClustersRequest, error) {
	logrus.Info("invoking describeCluster")
	request := tke.NewDescribeClustersRequest()
	request.Limit = tccommon.Int64Ptr(int64(20))
	request.ClusterIds = []*string{
		tccommon.StringPtr(state.ClusterID),
	}
	return request, nil
}

func storeState(info *types.ClusterInfo, state *state) error {
	bytes, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if info.Metadata == nil {
		info.Metadata = map[string]string{}
	}
	info.Metadata["state"] = string(bytes)
	info.Metadata["project-id"] = string(state.ProjectID)
	info.Metadata["zone"] = state.Region
	return nil
}

func getState(info *types.ClusterInfo) (*state, error) {
	state := &state{}
	// ignore error
	err := json.Unmarshal([]byte(info.Metadata["state"]), &state)
	return state, err
}

// Update implements driver update interface
func (d *Driver) Update(ctx context.Context, info *types.ClusterInfo, opts *types.DriverOptions) (*types.ClusterInfo, error) {
	logrus.Info("Invoking update cluster")
	state, err := getState(info)
	if err != nil {
		return nil, err
	}

	newState, err := getStateFromOpts(opts, false)
	if err != nil {
		return nil, err
	}

	svc, err := getTKEServiceClient(state, "POST")
	if err != nil {
		return nil, err
	}

	logrus.Debugf("Updating config, clusterName: %s, clusterVersion: %s, new node: %v", state.ClusterName, state.ClusterVersion, newState.GoodsNum)

	if state.NodeCount != newState.NodeCount {
		request := tke.NewDescribeClusterInstancesRequest()
		request.ClusterId = &state.ClusterID
		resp, err := svc.DescribeClusterInstances(request)
		if err != nil {
			return nil, fmt.Errorf("an API error has returned: %s", err)
		}
		nodeCount := resp.Response.TotalCount

		if newState.NodeCount > int64(*nodeCount) {

			log.Infof(ctx, "Scaling up cluster nodes to %d", newState.NodeCount)
			req, err := getWrapAddClusterInstancesRequest(state, newState, int64(*nodeCount))
			if err != nil {
				return nil, err
			}

			if _, err = svc.CreateClusterInstances(req); err != nil {
				return nil, err
			}

			logrus.Infof("Add cluster instances is called for cluster %s.", state.ClusterID)

			if err := waitTKECluster(ctx, svc, state); err != nil {
				return nil, err
			}
		} else if newState.NodeCount < int64(*nodeCount) {
			log.Infof(ctx, "Scaling down cluster nodes to %d", newState.NodeCount)

			req, err := removeClusterInstances(state, newState, resp)
			if err != nil {
				return nil, err
			}

			if _, err = svc.DeleteClusterInstances(req); err != nil {
				return nil, err
			}
		}
		state.NodeCount = newState.NodeCount
	}

	if newState.ClusterName != "" || newState.ClusterDesc != "" {
		log.Infof(ctx, "Updating cluster %s attributes to name: %s, desc: %s", state.ClusterName, newState.ClusterName, newState.ClusterDesc)
		req, err := getWrapModifyClusterAttributesRequest(state, newState)
		if err != nil {
			return nil, err
		}

		// init the TKE client
		if _, err = svc.ModifyClusterAttribute(req); err != nil {
			return nil, err
		}

		logrus.Infof("Modify cluster attributes is called for cluster %s.", state.ClusterID)

		if err := waitTKECluster(ctx, svc, state); err != nil {
			return nil, err
		}
		state.ClusterName = newState.ClusterName
		state.ClusterDesc = newState.ClusterDesc
	}

	if state.ProjectID != newState.ProjectID {
		log.Infof(ctx, "Updating project id to %d for cluster %s", newState.ProjectID, state.ClusterName)
		req, err := getWrapModifyProjectIDRequest(state, newState)
		if err != nil {
			return nil, err
		}

		// init the TKE client
		if _, err = svc.ModifyClusterAttribute(req); err != nil {
			return nil, err
		}

		logrus.Infof("Modify cluster projectId is called for cluster %s.", state.ClusterID)

		if err := waitTKECluster(ctx, svc, state); err != nil {
			return nil, err
		}
		state.ProjectID = newState.ProjectID
	}
	return info, storeState(info, state)
}

func removeClusterInstances(state, newState *state, instancesResp *tke.DescribeClusterInstancesResponse) (*tke.DeleteClusterInstancesRequest, error) {
	deleteCount := int64(*instancesResp.Response.TotalCount) - newState.NodeCount
	logrus.Debugf("invoking removeClusterInstances, delete node count: %d", deleteCount)

	var instanceIds = make([]*string, deleteCount)
	deleteNodes := instancesResp.Response.InstanceSet[:deleteCount]
	for i, node := range deleteNodes {
		instanceIds[i] = node.InstanceId
	}

	request := tke.NewDeleteClusterInstancesRequest()
	request.ClusterId = &state.ClusterID
	request.InstanceIds = instanceIds
	request.InstanceDeleteMode = &instanceDeleteModeTerminate

	return request, nil
}
func getClusterCerts(svc *tke.Client, state *state) (*tke.DescribeClusterSecurityResponse, error) {
	logrus.Info("invoking getClusterCerts")

	request := tke.NewDescribeClusterSecurityRequest()
	content, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}

	if err = request.FromJsonString(string(content)); err != nil {
		return nil, err
	}

	resp, err := svc.DescribeClusterSecurity(request)
	if err != nil {
		return resp, fmt.Errorf("an API error has returned: %s", err)
	}
	return resp, nil
}

// PostCheck implements driver postCheck interface
func (d *Driver) PostCheck(ctx context.Context, info *types.ClusterInfo) (*types.ClusterInfo, error) {
	logrus.Info("starting post-check")
	clientSet, err := getClientSet(ctx, info)
	if err != nil {
		return nil, err
	}
	failureCount := 0
	for {
		info.ServiceAccountToken, err = util.GenerateServiceAccountToken(clientSet)

		if err == nil {
			logrus.Info("service account token generated successfully")
			break
		} else {
			if failureCount < retries {
				logrus.Infof("service account token generation failed, retries left: %v", retries-failureCount)
				failureCount = failureCount + 1

				time.Sleep(pollInterval * time.Second)
			} else {
				logrus.Error("retries exceeded, failing post-check")
				return nil, err
			}
		}
	}
	logrus.Info("post-check completed successfully")
	return info, nil
}

// Remove implements driver remove interface
func (d *Driver) Remove(ctx context.Context, info *types.ClusterInfo) error {
	logrus.Info("invoking removeCluster")
	state, err := getState(info)
	if err != nil {
		return err
	}

	if state == nil || state.ClusterID == "" {
		logrus.Infof("Cluster %s clusterId doesn't exist", state.ClusterName)
		return nil
	}

	svc, err := getTKEServiceClient(state, "GET")
	if err != nil {
		return err
	}

	logrus.Debugf("Removing cluster %v from region %v, zone %v", state.ClusterName, state.Region, state.ZoneID)

	req, err := d.getWrapRemoveClusterRequest(state)
	if err != nil {
		return err
	}

	req.InstanceDeleteMode = &instanceDeleteModeTerminate

	_, err = svc.DeleteCluster(req)
	if err != nil && !strings.Contains(err.Error(), "NotFound") {
		return err
	} else if err == nil {
		logrus.Debugf("Cluster %v delete is called.", state.ClusterName)
	} else {
		logrus.Debugf("Cluster %s doesn't exist", state.ClusterName)
	}
	return nil
}

func (d *Driver) getWrapRemoveClusterRequest(state *state) (*tke.DeleteClusterRequest, error) {
	logrus.Info("invoking get remove cluster request")
	request := tke.NewDeleteClusterRequest()
	content, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}

	if err = request.FromJsonString(string(content)); err != nil {
		return nil, err
	}
	return request, nil
}

// GetCapabilities implements driver get capabilities interface
func (d *Driver) GetCapabilities(ctx context.Context) (*types.Capabilities, error) {
	return &d.driverCapabilities, nil
}

// GetClusterSize implements driver get cluster size interface
func (d *Driver) GetClusterSize(ctx context.Context, info *types.ClusterInfo) (*types.NodeCount, error) {
	state, err := getState(info)
	if err != nil {
		return nil, err
	}
	svc, err := getTKEServiceClient(state, "GET")
	if err != nil {
		return nil, err
	}
	clusters, err := getCluster(svc, state)
	if err != nil {
		return nil, err
	}
	return &types.NodeCount{Count: int64(*clusters.Response.Clusters[0].ClusterNodeNum)}, nil
}

// GetVersion implements driver get cluster kubernetes version interface
func (d *Driver) GetVersion(ctx context.Context, info *types.ClusterInfo) (*types.KubernetesVersion, error) {
	state, err := getState(info)
	if err != nil {
		return nil, err
	}
	svc, err := getTKEServiceClient(state, "GET")
	if err != nil {
		return nil, err
	}
	resp, err := getCluster(svc, state)
	if err != nil {
		return nil, err
	}
	return &types.KubernetesVersion{Version: *resp.Response.Clusters[0].ClusterVersion}, nil
}

// operateClusterVip creates or remove the cluster vip
func operateClusterVip(ctx context.Context, svc *tke.Client, clusterID, operation string) error {
	logrus.Info("invoking operateClusterVip")

	req := tke.NewCreateClusterEndpointVipRequest()
	req.ClusterId = &clusterID
	// make all request can be through
	req.SecurityPolicies = tccommon.StringPtrs([]string{"0.0.0.0/0"})

	reqStatus := tke.NewDescribeClusterEndpointVipStatusRequest()
	reqStatus.ClusterId = &clusterID

	if _, err := svc.CreateClusterEndpointVip(req); err != nil {
		return fmt.Errorf("an API error has returned: %s", err)
	}

	count := 0
	for {
		respStatus, err := svc.DescribeClusterEndpointVipStatus(reqStatus)
		if err != nil {
			return fmt.Errorf("an API error has returned: %s", err)
		}

		if *respStatus.Response.Status == successStatus && count >= 1 {
			return nil
		} else if *respStatus.Response.Status == failedStatus {
			return fmt.Errorf("describe cluster endpoint vip status: %s", err)
		}
		count++
		time.Sleep(time.Second * 15)
	}
}

// SetClusterSize implements driver set cluster size interface
func (d *Driver) SetClusterSize(ctx context.Context, info *types.ClusterInfo, count *types.NodeCount) error {
	logrus.Info("unimplemented")
	return nil
}

// SetVersion implements driver set cluster kubernetes version interface
func (d *Driver) SetVersion(ctx context.Context, info *types.ClusterInfo, version *types.KubernetesVersion) error {
	logrus.Info("unimplemented")
	return nil
}

// RemoveLegacyServiceAccount remove any old service accounts that the driver has created
func (d *Driver) RemoveLegacyServiceAccount(ctx context.Context, info *types.ClusterInfo) error {
	clientSet, err := getClientSet(ctx, info)
	if err != nil {
		return err
	}

	return util.DeleteLegacyServiceAccountAndRoleBinding(clientSet)
}

// getClientSet returns cluster clientSet
func getClientSet(ctx context.Context, info *types.ClusterInfo) (kubernetes.Interface, error) {
	state, err := getState(info)
	if err != nil {
		return nil, err
	}
	svc, err := getTKEServiceClient(state, "GET")
	if err != nil {
		return nil, err
	}

	if err := waitTKECluster(ctx, svc, state); err != nil {
		return nil, err
	}

	cluster, err := getCluster(svc, state)
	if err != nil {
		return nil, err
	}

	certs, err := getClusterCerts(svc, state)
	if err != nil {
		return nil, err
	}

	if *certs.Response.ClusterExternalEndpoint == "" {
		err := operateClusterVip(ctx, svc, state.ClusterID, "Create")
		if err != nil {
			return nil, err
		}

		// update cluster certs with new generated cluster vip
		certs, err = getClusterCerts(svc, state)
		if err != nil {
			return nil, err
		}
	}

	info.Version = *cluster.Response.Clusters[0].ClusterVersion
	info.Endpoint = *certs.Response.ClusterExternalEndpoint
	info.RootCaCertificate = base64.StdEncoding.EncodeToString([]byte(*certs.Response.CertificationAuthority))
	info.Username = *certs.Response.UserName
	info.Password = *certs.Response.Password
	info.NodeCount = int64(*cluster.Response.Clusters[0].ClusterNodeNum)
	info.Status = *cluster.Response.Clusters[0].ClusterStatus

	host := info.Endpoint
	if !strings.HasPrefix(host, "https://") {
		host = fmt.Sprintf("https://%s", host)
	}

	config := &rest.Config{
		Host:     host,
		Username: *certs.Response.UserName,
		Password: *certs.Response.Password,
		TLSClientConfig: rest.TLSClientConfig{
			CAData: []byte(*certs.Response.CertificationAuthority),
		},
	}
	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("error creating clientset: %v", err)
	}
	return clientSet, nil
}

// ETCDSave will backup etcd snapshot to s3 server
func (d *Driver) ETCDSave(ctx context.Context, info *types.ClusterInfo, opts *types.DriverOptions, snapshotName string) error {
	return fmt.Errorf("ETCD backup operations are not implemented")
}

// ETCDRestore will restore etcd snapshot from s3 server
func (d *Driver) ETCDRestore(ctx context.Context, info *types.ClusterInfo, opts *types.DriverOptions, snapshotName string) error {
	return fmt.Errorf("ETCD backup operations are not implemented")
}

// GetK8SCapabilities defines TKE k8s capabilities
func (d *Driver) GetK8SCapabilities(ctx context.Context, opts *types.DriverOptions) (*types.K8SCapabilities, error) {
	capabilities := &types.K8SCapabilities{
		L4LoadBalancer: &types.LoadBalancerCapabilities{
			Enabled:              true,
			Provider:             "Tencent Cloud L4 LB",
			ProtocolsSupported:   []string{"TCP", "UDP"},
			HealthCheckSupported: true,
		},
	}

	capabilities.IngressControllers = []*types.IngressCapabilities{
		{
			IngressProvider:      "Tencent Cloud Ingress",
			CustomDefaultBackend: true,
		},
	}
	return capabilities, nil
}

func getWrapAddClusterInstancesRequest(state, newState *state, nodeCount int64) (*tke.CreateClusterInstancesRequest, error) {
	logrus.Debugf("invoking get wrap request of AddClusterInstances")
	// goodsNum is the new nodes count that will be added to the cluster
	state.GoodsNum = newState.NodeCount - nodeCount

	if newState.InstanceType != "" {
		state.InstanceType = newState.InstanceType
	}
	if newState.BandwidthType != "" {
		state.BandwidthType = newState.BandwidthType
	}
	if newState.Bandwidth != 0 {
		state.Bandwidth = newState.Bandwidth
	}
	if newState.WanIP != 0 {
		state.WanIP = newState.WanIP
	}
	if newState.IsVpcGateway != 0 {
		state.IsVpcGateway = newState.IsVpcGateway
	}
	if newState.StorageSize != 0 {
		state.StorageSize = newState.StorageSize
	}
	if newState.RootSize != 0 {
		state.RootSize = newState.RootSize
	}
	if newState.SecretID != "" {
		state.SecretID = newState.SecretID
	}
	if newState.SecretKey != "" {
		state.SecretKey = newState.SecretKey
	}

	request := tke.NewCreateClusterInstancesRequest()

	stringRunInstancesPara, err := getRunInstancesPara(state)
	if err != nil {
		return nil, err
	}

	request.ClusterId = &state.ClusterID
	request.RunInstancePara = &stringRunInstancesPara

	return request, nil
}

func getWrapModifyClusterAttributesRequest(state, newState *state) (*tke.ModifyClusterAttributeRequest, error) {
	logrus.Debugf("invoking get wrap request of ModifyClusterAttributes")
	if newState.ClusterName != "" {
		state.ClusterName = newState.ClusterName
	}
	if newState.ClusterDesc != "" {
		state.ClusterDesc = newState.ClusterDesc
	}
	if newState.SecretID != "" {
		state.SecretID = newState.SecretID
	}
	if newState.SecretKey != "" {
		state.SecretKey = newState.SecretKey
	}

	request := tke.NewModifyClusterAttributeRequest()
	request.ClusterId = &state.ClusterID
	request.ClusterName = &state.ClusterName
	request.ClusterDesc = &state.ClusterDesc

	return request, nil
}

func getWrapModifyProjectIDRequest(state, newState *state) (*tke.ModifyClusterAttributeRequest, error) {
	logrus.Debugf("invoking get wrap request of ModifyProjectId")
	if state.ProjectID != newState.ProjectID {
		state.ProjectID = newState.ProjectID
	}
	if newState.SecretID != "" {
		state.SecretID = newState.SecretID
	}
	if newState.SecretKey != "" {
		state.SecretKey = newState.SecretKey
	}

	request := tke.NewModifyClusterAttributeRequest()
	request.ClusterId = &state.ClusterID
	request.ProjectId = &state.ProjectID

	return request, nil
}

func getBoolean(value int64) bool {
	return value == 1
}

func getInstanceChargeType(value string) string {
	instanceChargeType := value
	switch value {
	case "PayByHour":
		instanceChargeType = "POSTPAID_BY_HOUR"
	case "PayByMonth":
		instanceChargeType = "PREPAID"
	default:
		instanceChargeType = "POSTPAID_BY_HOUR"
	}

	return instanceChargeType
}

func getInternetChargeType(value string) string {
	internetChargeType := value
	switch value {
	case "PayByHour":
		internetChargeType = "BANDWIDTH_POSTPAID_BY_HOUR"
	case "PayByTraffic":
		internetChargeType = "TRAFFIC_POSTPAID_BY_HOUR"
	}

	return internetChargeType
}

func getZone(state *state) (string, error) {
	zone := state.ZoneID
	svc, err := getCVMServiceClient(state, "GET")
	if err != nil {
		return zone, err
	}

	request := cvm.NewDescribeZonesRequest()
	response, err := svc.DescribeZones(request)
	if err != nil {
		return zone, err
	}

	for _, item := range response.Response.ZoneSet {
		if *item.ZoneId == state.ZoneID {
			zone = *item.Zone
		}
	}

	return zone, nil
}
