package awsclient

import (
	"fmt"

	"github.com/aws/aws-sdk-go/service/elbv2"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/elb"
)

// AWSLoadBalancer a handy way to return information about an ELB
type AWSLoadBalancer struct {
	ELBName   string // Name of the ELB
	DNSName   string // DNS Name
	DNSZoneId string // Zone ID
}

// CreateClassicELB creates a classic ELB in Amazon, as in for management API endpoint.
// inputs are the name of the ELB, the availability zone(s) and subnet(s) the
// ELB should attend, as well as the listener port.
// The port is used for the instance port and load balancer port
// Return is the (FQDN) DNS name from Amazon, and error, if any.
func (c *awsClient) CreateClassicELB(elbName string, subnets []string, listenerPort int64) (*AWSLoadBalancer, error) {
	fmt.Printf("  * CreateClassicELB(%s,%s,%d)\n", elbName, subnets, listenerPort)
	i := &elb.CreateLoadBalancerInput{
		LoadBalancerName: aws.String(elbName),
		Subnets:          aws.StringSlice(subnets),
		//AvailabilityZones: aws.StringSlice(availabilityZones),
		Listeners: []*elb.Listener{
			{
				InstancePort:     aws.Int64(listenerPort),
				InstanceProtocol: aws.String("tcp"),
				Protocol:         aws.String("tcp"),
				LoadBalancerPort: aws.Int64(listenerPort),
			},
		},
	}
	_, err := c.CreateLoadBalancer(i)
	if err != nil {
		return &AWSLoadBalancer{}, err
	}
	fmt.Printf("    * Adding health check (HTTP:6443/)\n")
	err = c.addHealthCheck(elbName, "HTTP", "/", 6443)
	if err != nil {
		return &AWSLoadBalancer{}, err
	}
	// Caller will need the DNS name and Zone ID for the ELB (for route53) so let's make a handy object to return, using the
	_, awsELBObj, err := c.DoesELBExist(elbName)
	if err != nil {
		return &AWSLoadBalancer{}, err
	}
	return awsELBObj, nil
}

// SetLoadBalancerPrivate sets a load balancer private by removing its
// listeners (port 6443/TCP)
func (c *awsClient) SetLoadBalancerPrivate(elbName string) error {
	return c.removeListenersFromELB(elbName)
}

// SetLoadBalancerPublic will set the specified load balancer public by
// re-adding the 6443/TCP -> 6443/TCP listener. Any instances (still)
// attached to the load balancer will begin to receive traffic.
func (c *awsClient) SetLoadBalancerPublic(elbName string, listenerPort int64) error {
	l := []*elb.Listener{
		{
			InstancePort:     aws.Int64(listenerPort),
			InstanceProtocol: aws.String("tcp"),
			Protocol:         aws.String("tcp"),
			LoadBalancerPort: aws.Int64(listenerPort),
		},
	}
	return c.addListenersToELB(elbName, l)
}

// removeListenersFromELB will remove the 6443/TCP -> 6443/TCP listener from
// the specified ELB. This is useful when the "ext" ELB is to be no longer
// publicly accessible
func (c *awsClient) removeListenersFromELB(elbName string) error {
	i := &elb.DeleteLoadBalancerListenersInput{
		LoadBalancerName:  aws.String(elbName),
		LoadBalancerPorts: aws.Int64Slice([]int64{6443}),
	}
	_, err := c.DeleteLoadBalancerListeners(i)
	return err
}

// addListenersToELB will add the +listeners+ to the specified ELB. This is
// useful for when the "ext" ELB is to be publicly accessible. See also
// removeListenersFromELB.
// Note: This will likely always want to be given 6443/tcp -> 6443/tcp for
// the kube-api
func (c *awsClient) addListenersToELB(elbName string, listeners []*elb.Listener) error {
	i := &elb.CreateLoadBalancerListenersInput{
		Listeners:        listeners,
		LoadBalancerName: aws.String(elbName),
	}
	_, err := c.CreateLoadBalancerListeners(i)
	return err
}

// AddLoadBalancerInstances will attach +instanceIds+ to +elbName+
// so that they begin to receive traffic. Note that this takes an amount of
// time to return. This is also additive (but idempotent - TODO: Validate this).
// Note that the recommended steps:
// 1. stop the instance,
// 2. deregister the instance,
// 3. start the instance,
// 4. and then register the instance.
func (c *awsClient) AddLoadBalancerInstances(elbName string, instanceIds []string) error {
	instances := make([]*elb.Instance, 0)
	for _, instance := range instanceIds {
		instances = append(instances, &elb.Instance{InstanceId: aws.String(instance)})
	}
	i := &elb.RegisterInstancesWithLoadBalancerInput{
		Instances:        instances,
		LoadBalancerName: aws.String(elbName),
	}
	_, err := c.RegisterInstancesWithLoadBalancer(i)
	return err
}

// RemoveInstancesFromLoadBalancer removes +instanceIds+ from +elbName+, eg when an Node is deleted.
func (c *awsClient) RemoveInstancesFromLoadBalancer(elbName string, instanceIds []string) error {
	instances := make([]*elb.Instance, 0)
	for _, instance := range instanceIds {
		instances = append(instances, &elb.Instance{InstanceId: aws.String(instance)})
	}
	i := &elb.DeregisterInstancesFromLoadBalancerInput{
		Instances:        instances,
		LoadBalancerName: aws.String(elbName),
	}
	_, err := c.DeregisterInstancesFromLoadBalancer(i)
	return err
}

// DoesELBExist checks for the existence of an ELB by name. If there's an AWS
// error it is returned.
func (c *awsClient) DoesELBExist(elbName string) (bool, *AWSLoadBalancer, error) {
	i := &elb.DescribeLoadBalancersInput{
		LoadBalancerNames: []*string{aws.String(elbName)},
	}
	res, err := c.DescribeLoadBalancers(i)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case elb.ErrCodeAccessPointNotFoundException:
				return false, &AWSLoadBalancer{}, nil
			default:
				return false, &AWSLoadBalancer{}, err
			}
		}
	}
	return true, &AWSLoadBalancer{ELBName: elbName, DNSName: *res.LoadBalancerDescriptions[0].DNSName, DNSZoneId: *res.LoadBalancerDescriptions[0].CanonicalHostedZoneNameID}, nil
}

// LoadBalancerV2 is a list of all non-classic ELBs
type LoadBalancerV2 struct {
	CanonicalHostedZoneNameID string
	DNSName                   string
	LoadBalancerArn           string
	LoadBalancerName          string
	Scheme                    string
	VpcID                     string
}

// ListAllNLBs uses the DescribeLoadBalancersV2 to get back a list of all Network Load Balancers
func (c *awsClient) ListAllNLBs() ([]LoadBalancerV2, error) {

	i := &elbv2.DescribeLoadBalancersInput{}
	output, err := c.DescribeLoadBalancersV2(i)
	if err != nil {
		return []LoadBalancerV2{}, err
	}
	loadBalancers := make([]LoadBalancerV2, 0)
	for _, loadBalancer := range output.LoadBalancers {
		loadBalancers = append(loadBalancers, LoadBalancerV2{
			CanonicalHostedZoneNameID: aws.StringValue(loadBalancer.CanonicalHostedZoneId),
			DNSName:                   aws.StringValue(loadBalancer.DNSName),
			LoadBalancerArn:           aws.StringValue(loadBalancer.LoadBalancerArn),
			LoadBalancerName:          aws.StringValue(loadBalancer.LoadBalancerName),
			Scheme:                    aws.StringValue(loadBalancer.Scheme),
			VpcID:                     aws.StringValue(loadBalancer.VpcId),
		})
	}
	return loadBalancers, nil
}

// DeleteExternalLoadBalancer takes in the external LB arn and deletes the entire LB
func (c *awsClient) DeleteExternalLoadBalancer(extLoadBalancerArn string) error {
	i := elbv2.DeleteLoadBalancerInput{
		LoadBalancerArn: aws.String(extLoadBalancerArn),
	}
	_, err := c.DeleteLoadBalancerV2(&i)
	return err
}

// CreateNetworkLoadBalancer should only return one new NLB at a time
func (c *awsClient) CreateNetworkLoadBalancer(lbName, scheme, subnet string) ([]LoadBalancerV2, error) {
	i := &elbv2.CreateLoadBalancerInput{
		Name:   aws.String(lbName),
		Scheme: aws.String(scheme),
		Subnets: []*string{
			aws.String(subnet),
		},
		Type: aws.String("network"),
	}

	result, err := c.CreateLoadBalancerV2(i)
	if err != nil {
		return []LoadBalancerV2{}, err
	}

	// there should only be 1 NLB made, but since CreateLoadBalancerOutput takes in slice
	// we return it as slice
	loadBalancers := make([]LoadBalancerV2, 0)
	for _, loadBalancer := range result.LoadBalancers {
		loadBalancers = append(loadBalancers, LoadBalancerV2{
			CanonicalHostedZoneNameID: aws.StringValue(loadBalancer.CanonicalHostedZoneId),
			DNSName:                   aws.StringValue(loadBalancer.DNSName),
			LoadBalancerArn:           aws.StringValue(loadBalancer.LoadBalancerArn),
			LoadBalancerName:          aws.StringValue(loadBalancer.LoadBalancerName),
			Scheme:                    aws.StringValue(loadBalancer.Scheme),
			VpcID:                     aws.StringValue(loadBalancer.VpcId),
		})
	}
	return loadBalancers, nil
}

// create the external NLB target group and returns the targetGroupArn
func (c *awsClient) CreateExternalNLBTargetGroup(nlbName, vpcID string) (string, error) {
	i := &elbv2.CreateTargetGroupInput{
		Name:                       aws.String(nlbName),
		Port:                       aws.Int64(6443),
		Protocol:                   aws.String("TCP"),
		TargetType:                 aws.String("ip"),
		VpcId:                      aws.String(vpcID),
		HealthCheckPath:            aws.String("/readyz"),
		HealthCheckPort:            aws.String("6443"),
		HealthCheckProtocol:        aws.String("HTTPS"),
		HealthCheckIntervalSeconds: aws.Int64(10),
		HealthCheckTimeoutSeconds:  aws.Int64(10),
		HealthyThresholdCount:      aws.Int64(2),
		UnhealthyThresholdCount:    aws.Int64(2),
	}

	result, err := c.CreateTargetGroupV2(i)
	if err != nil {
		return "", err
	}

	return aws.StringValue(result.TargetGroups[0].TargetGroupArn), nil
}

// type TargetDescription struct {
// 	AvailabilityZone string
// 	Id string
// 	Port string
// }

// func (c *awsClient) RegisterMasterNodeIPs(targetGroupArn string, ) error {
// 	i := &elbv2.RegisterTargetsInput{
// 		TargetGroupArn: aws.String(targetGroupArn),
// 		Targets: []*elbv2.TargetDescription{
// 			{

// 			}
// 		}
// 	}
// }

func (c *awsClient) addHealthCheck(loadBalancerName, protocol, path string, port int64) error {
	i := &elb.ConfigureHealthCheckInput{
		HealthCheck: &elb.HealthCheck{
			HealthyThreshold:   aws.Int64(2),
			Interval:           aws.Int64(30),
			Target:             aws.String(fmt.Sprintf("%s:%d%s", protocol, port, path)),
			Timeout:            aws.Int64(3),
			UnhealthyThreshold: aws.Int64(2),
		},
		LoadBalancerName: aws.String(loadBalancerName),
	}
	_, err := c.ConfigureHealthCheck(i)
	return err
}
