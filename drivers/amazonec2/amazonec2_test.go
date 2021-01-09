package amazonec2

import (
	"errors"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/docker/machine/commands/commandstest"
)

const (
	testSSHPort    = int64(22)
	testDockerPort = int64(2376)
	testSwarmPort  = int64(3376)
)

var (
	securityGroup = &ec2.SecurityGroup{
		GroupName: aws.String("test-group"),
		GroupId:   aws.String("12345"),
		VpcId:     aws.String("12345"),
	}
)

func TestConfigureSecurityGroupPermissionsEmpty(t *testing.T) {
	driver := NewTestDriver()

	perms, err := driver.configureSecurityGroupPermissions(securityGroup)

	assert.Nil(t, err)
	assert.Len(t, perms, 2)
}

func setupTestFailedSpotInstanceRequest(t *testing.T, r *ec2.RequestSpotInstancesOutput) (*Driver, func()) {
	dir, _ := ioutil.TempDir("", "awsuserdata")
	privKeyPath := filepath.Join(dir, "key")
	pubKeyPath := filepath.Join(dir, "key.pub")

	content := []byte("test\n")
	err := ioutil.WriteFile(privKeyPath, content, 0666)
	assert.NoError(t, err)
	err = ioutil.WriteFile(pubKeyPath, content, 0666)
	assert.NoError(t, err)

	driver := NewDriver("machineFoo", "path")
	driver.clientFactory = func() Ec2Client {
		return &fakeEC2SpotInstance{
			output: r,
			err:    nil,
		}
	}

	driver.SecurityGroupNames = []string{}

	driver.SSHPrivateKeyPath = privKeyPath
	driver.SSHKeyPath = pubKeyPath
	driver.KeyName = "foo"

	driver.RequestSpotInstance = true

	return driver, func() {
		os.RemoveAll(dir)
	}
}

func TestFailedSpotInstanceRequest(t *testing.T) {
	var failureScenarios = []struct {
		requestResult *ec2.RequestSpotInstancesOutput
	}{
		{requestResult: nil},
		{
			requestResult: &ec2.RequestSpotInstancesOutput{
				SpotInstanceRequests: []*ec2.SpotInstanceRequest{},
			},
		},
	}

	for _, tt := range failureScenarios {
		driver, cleanup := setupTestFailedSpotInstanceRequest(t, tt.requestResult)
		defer cleanup()

		err := driver.innerCreate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), errorUnprocessableResponse.Error())
	}
}

func TestConfigureSecurityGroupPermissionsSshOnly(t *testing.T) {
	driver := NewTestDriver()
	group := securityGroup
	group.IpPermissions = []*ec2.IpPermission{
		{
			IpProtocol: aws.String("tcp"),
			FromPort:   aws.Int64(int64(testSSHPort)),
			ToPort:     aws.Int64(int64(testSSHPort)),
		},
	}

	perms, err := driver.configureSecurityGroupPermissions(group)

	assert.Nil(t, err)
	assert.Len(t, perms, 1)
	assert.Equal(t, testDockerPort, *perms[0].FromPort)
}

func TestConfigureSecurityGroupPermissionsDockerOnly(t *testing.T) {
	driver := NewTestDriver()
	group := securityGroup
	group.IpPermissions = []*ec2.IpPermission{
		{
			IpProtocol: aws.String("tcp"),
			FromPort:   aws.Int64((testDockerPort)),
			ToPort:     aws.Int64((testDockerPort)),
		},
	}

	perms, err := driver.configureSecurityGroupPermissions(group)

	assert.Nil(t, err)
	assert.Len(t, perms, 1)
	assert.Equal(t, testSSHPort, *perms[0].FromPort)
}

func TestConfigureSecurityGroupPermissionsDockerAndSsh(t *testing.T) {
	driver := NewTestDriver()
	group := securityGroup
	group.IpPermissions = []*ec2.IpPermission{
		{
			IpProtocol: aws.String("tcp"),
			FromPort:   aws.Int64(testSSHPort),
			ToPort:     aws.Int64(testSSHPort),
		},
		{
			IpProtocol: aws.String("tcp"),
			FromPort:   aws.Int64(testDockerPort),
			ToPort:     aws.Int64(testDockerPort),
		},
	}

	perms, err := driver.configureSecurityGroupPermissions(group)

	assert.Nil(t, err)
	assert.Empty(t, perms)
}

func TestConfigureSecurityGroupPermissionsSkipReadOnly(t *testing.T) {
	driver := NewTestDriver()
	driver.SecurityGroupReadOnly = true
	perms, err := driver.configureSecurityGroupPermissions(securityGroup)

	assert.Nil(t, err)
	assert.Len(t, perms, 0)
}

func TestConfigureSecurityGroupPermissionsOpenPorts(t *testing.T) {
	driver := NewTestDriver()
	driver.OpenPorts = []string{"8888/tcp", "8080/udp", "9090"}
	perms, err := driver.configureSecurityGroupPermissions(&ec2.SecurityGroup{})

	assert.NoError(t, err)
	assert.Len(t, perms, 5)
	assert.Equal(t, aws.Int64(int64(8888)), perms[2].ToPort)
	assert.Equal(t, aws.String("tcp"), perms[2].IpProtocol)
	assert.Equal(t, aws.Int64(int64(8080)), perms[3].ToPort)
	assert.Equal(t, aws.String("udp"), perms[3].IpProtocol)
	assert.Equal(t, aws.Int64(int64(9090)), perms[4].ToPort)
	assert.Equal(t, aws.String("tcp"), perms[4].IpProtocol)
}

func TestConfigureSecurityGroupPermissionsOpenPortsSkipExisting(t *testing.T) {
	driver := NewTestDriver()
	group := securityGroup
	group.IpPermissions = []*ec2.IpPermission{
		{
			IpProtocol: aws.String("tcp"),
			FromPort:   aws.Int64(8888),
			ToPort:     aws.Int64(testSSHPort),
		},
		{
			IpProtocol: aws.String("tcp"),
			FromPort:   aws.Int64(8080),
			ToPort:     aws.Int64(testSSHPort),
		},
	}
	driver.OpenPorts = []string{"8888/tcp", "8080/udp", "8080"}
	perms, err := driver.configureSecurityGroupPermissions(group)
	assert.NoError(t, err)
	assert.Len(t, perms, 3)
	assert.Equal(t, aws.Int64(int64(8080)), perms[2].ToPort)
	assert.Equal(t, aws.String("udp"), perms[2].IpProtocol)
}

func TestConfigureSecurityGroupPermissionsInvalidOpenPorts(t *testing.T) {
	driver := NewTestDriver()
	driver.OpenPorts = []string{"2222/tcp", "abc1"}
	perms, err := driver.configureSecurityGroupPermissions(&ec2.SecurityGroup{})

	assert.Error(t, err)
	assert.Nil(t, perms)
}

func TestConfigureSecurityGroupPermissionsWithSwarm(t *testing.T) {
	driver := NewTestDriver()
	driver.SwarmMaster = true
	group := securityGroup
	group.IpPermissions = []*ec2.IpPermission{
		{
			IpProtocol: aws.String("tcp"),
			FromPort:   aws.Int64(testSSHPort),
			ToPort:     aws.Int64(testSSHPort),
		},
		{
			IpProtocol: aws.String("tcp"),
			FromPort:   aws.Int64(testDockerPort),
			ToPort:     aws.Int64(testDockerPort),
		},
	}

	perms, err := driver.configureSecurityGroupPermissions(group)

	assert.Nil(t, err)
	assert.Len(t, perms, 1)
	assert.Equal(t, testSwarmPort, *perms[0].FromPort)
}

func TestValidateAwsRegionValid(t *testing.T) {
	regions := []string{"eu-west-1", "eu-central-1"}

	for _, region := range regions {
		validatedRegion, err := validateAwsRegion(region)

		assert.NoError(t, err)
		assert.Equal(t, region, validatedRegion)
	}
}

func TestValidateAwsRegionInvalid(t *testing.T) {
	regions := []string{"eu-central-2"}

	for _, region := range regions {
		_, err := validateAwsRegion(region)

		assert.EqualError(t, err, "Invalid region specified")
	}
}

func TestFindDefaultVPC(t *testing.T) {
	driver := NewDriver("machineFoo", "path")
	driver.clientFactory = func() Ec2Client {
		return &fakeEC2WithLogin{}
	}

	vpc, err := driver.getDefaultVPCId()

	assert.Equal(t, "vpc-9999", vpc)
	assert.NoError(t, err)
}

func TestDefaultVPCIsMissing(t *testing.T) {
	driver := NewDriver("machineFoo", "path")
	driver.clientFactory = func() Ec2Client {
		return &fakeEC2WithDescribe{
			output: &ec2.DescribeAccountAttributesOutput{
				AccountAttributes: []*ec2.AccountAttribute{},
			},
		}
	}

	vpc, err := driver.getDefaultVPCId()

	assert.EqualError(t, err, "No default-vpc attribute")
	assert.Empty(t, vpc)
}

func TestDefaultVPCIsNone(t *testing.T) {
	driver := NewDriver("machineFoo", "path")
	attributeName := "default-vpc"
	vpcName := "none"
	driver.clientFactory = func() Ec2Client {
		return &fakeEC2WithDescribe{
			output: &ec2.DescribeAccountAttributesOutput{
				AccountAttributes: []*ec2.AccountAttribute{
					{
						AttributeName: &attributeName,
						AttributeValues: []*ec2.AccountAttributeValue{
							{AttributeValue: &vpcName},
						},
					},
				},
			},
		}
	}

	vpc, err := driver.getDefaultVPCId()

	assert.EqualError(t, err, "default-vpc is 'none'")
	assert.Empty(t, vpc)
}

func TestGetRegionZoneForDefaultEndpoint(t *testing.T) {
	driver := NewCustomTestDriver(&fakeEC2WithLogin{})
	driver.awsCredentialsFactory = NewValidAwsCredentials
	options := &commandstest.FakeFlagger{
		Data: map[string]interface{}{
			"name":             "test",
			"amazonec2-region": "us-east-1",
			"amazonec2-zone":   "e",
		},
	}

	err := driver.SetConfigFromFlags(options)

	regionZone := driver.getRegionZone()

	assert.Equal(t, "us-east-1e", regionZone)
	assert.NoError(t, err)
}

func TestMetadataTokenSetting(t *testing.T) {
	driver := NewCustomTestDriver(&fakeEC2WithLogin{})
	driver.awsCredentialsFactory = NewValidAwsCredentials
	options := &commandstest.FakeFlagger{
		Data: map[string]interface{}{
			"name":                     "test",
			"amazonec2-metadata-token": "optional",
			"amazonec2-region":         "us-east-1",
			"amazonec2-zone":           "e",
		},
	}

	err := driver.SetConfigFromFlags(options)

	assert.Equal(t, "optional", driver.MetadataTokenSetting)
	assert.NoError(t, err)
}

func TestMetadataTokenResponseHopLimit(t *testing.T) {
	driver := NewCustomTestDriver(&fakeEC2WithLogin{})
	driver.awsCredentialsFactory = NewValidAwsCredentials
	options := &commandstest.FakeFlagger{
		Data: map[string]interface{}{
			"name": "test",
			"amazonec2-metadata-token-response-hop-limit": 1,
			"amazonec2-region":                            "us-east-1",
			"amazonec2-zone":                              "e",
		},
	}

	err := driver.SetConfigFromFlags(options)

	assert.Equal(t, int64(1), driver.MetadataTokenResponseHopLimit)
	assert.NoError(t, err)
}

func TestGetRegionZoneForCustomEndpoint(t *testing.T) {
	driver := NewCustomTestDriver(&fakeEC2WithLogin{})
	driver.awsCredentialsFactory = NewValidAwsCredentials
	options := &commandstest.FakeFlagger{
		Data: map[string]interface{}{
			"name":               "test",
			"amazonec2-endpoint": "https://someurl",
			"amazonec2-region":   "custom-endpoint",
			"amazonec2-zone":     "custom-zone",
		},
	}

	err := driver.SetConfigFromFlags(options)

	regionZone := driver.getRegionZone()

	assert.Equal(t, "custom-zone", regionZone)
	assert.NoError(t, err)
}

func TestDescribeAccountAttributeFails(t *testing.T) {
	driver := NewDriver("machineFoo", "path")
	driver.clientFactory = func() Ec2Client {
		return &fakeEC2WithDescribe{
			err: errors.New("Not Found"),
		}
	}

	vpc, err := driver.getDefaultVPCId()

	assert.EqualError(t, err, "Not Found")
	assert.Empty(t, vpc)
}

func TestAwsCredentialsAreRequired(t *testing.T) {
	driver := NewTestDriver()
	driver.awsCredentialsFactory = NewErrorAwsCredentials

	options := &commandstest.FakeFlagger{
		Data: map[string]interface{}{
			"name":             "test",
			"amazonec2-region": "us-east-1",
			"amazonec2-zone":   "e",
		},
	}

	err := driver.SetConfigFromFlags(options)
	assert.Equal(t, err, errorMissingCredentials)
}

func TestValidAwsCredentialsAreAccepted(t *testing.T) {
	driver := NewCustomTestDriver(&fakeEC2WithLogin{})
	driver.awsCredentialsFactory = NewValidAwsCredentials
	options := &commandstest.FakeFlagger{
		Data: map[string]interface{}{
			"name":             "test",
			"amazonec2-region": "us-east-1",
			"amazonec2-zone":   "e",
		},
	}

	err := driver.SetConfigFromFlags(options)
	assert.NoError(t, err)
}

func TestEndpointIsMandatoryWhenSSLDisabled(t *testing.T) {
	driver := NewTestDriver()
	driver.awsCredentialsFactory = NewValidAwsCredentials
	options := &commandstest.FakeFlagger{
		Data: map[string]interface{}{
			"name":                         "test",
			"amazonec2-access-key":         "foobar",
			"amazonec2-region":             "us-east-1",
			"amazonec2-zone":               "e",
			"amazonec2-insecure-transport": true,
		},
	}

	err := driver.SetConfigFromFlags(options)

	assert.Equal(t, err, errorDisableSSLWithoutCustomEndpoint)
}

var values = []string{
	"bob",
	"jake",
	"jill",
}

var pointerSliceTests = []struct {
	input    []string
	expected []*string
}{
	{[]string{}, []*string{}},
	{[]string{values[1]}, []*string{&values[1]}},
	{[]string{values[0], values[2], values[2]}, []*string{&values[0], &values[2], &values[2]}},
}

func TestMakePointerSlice(t *testing.T) {
	for _, tt := range pointerSliceTests {
		actual := makePointerSlice(tt.input)
		assert.Equal(t, tt.expected, actual)
	}
}

var securityGroupNameTests = []struct {
	groupName  string
	groupNames []string
	expected   []string
}{
	{groupName: "bob", expected: []string{"bob"}},
	{groupNames: []string{"bill"}, expected: []string{"bill"}},
	{groupName: "bob", groupNames: []string{"bill"}, expected: []string{"bob", "bill"}},
}

func TestMergeSecurityGroupName(t *testing.T) {
	for _, tt := range securityGroupNameTests {
		d := Driver{SecurityGroupName: tt.groupName, SecurityGroupNames: tt.groupNames}
		assert.Equal(t, tt.expected, d.securityGroupNames())
	}
}

var securityGroupIdTests = []struct {
	groupId  string
	groupIds []string
	expected []string
}{
	{groupId: "id", expected: []string{"id"}},
	{groupIds: []string{"id"}, expected: []string{"id"}},
	{groupId: "id1", groupIds: []string{"id2"}, expected: []string{"id1", "id2"}},
}

func TestMergeSecurityGroupId(t *testing.T) {
	for _, tt := range securityGroupIdTests {
		d := Driver{SecurityGroupId: tt.groupId, SecurityGroupIds: tt.groupIds}
		assert.Equal(t, tt.expected, d.securityGroupIds())
	}
}

func matchGroupLookup(expected []string) interface{} {
	return func(input *ec2.DescribeSecurityGroupsInput) bool {
		actual := []string{}
		for _, filter := range input.Filters {
			if *filter.Name == "group-name" {
				for _, groupName := range filter.Values {
					actual = append(actual, *groupName)
				}
			}
		}
		return reflect.DeepEqual(expected, actual)
	}
}

func ipPermission(port int64) *ec2.IpPermission {
	return &ec2.IpPermission{
		FromPort:   aws.Int64(port),
		ToPort:     aws.Int64(port),
		IpProtocol: aws.String("tcp"),
		IpRanges:   []*ec2.IpRange{{CidrIp: aws.String(ipRange)}},
	}
}

func TestConfigureSecurityGroupsEmpty(t *testing.T) {
	recorder := fakeEC2SecurityGroupTestRecorder{}

	driver := NewCustomTestDriver(&recorder)
	err := driver.configureSecurityGroups([]string{})

	assert.Nil(t, err)
	recorder.AssertExpectations(t)
}

func TestConfigureSecurityGroupsMixed(t *testing.T) {
	groups := []string{"existingGroup", "newGroup"}
	recorder := fakeEC2SecurityGroupTestRecorder{}

	// First, a check is made for which groups already exist.
	initialLookupResult := ec2.DescribeSecurityGroupsOutput{SecurityGroups: []*ec2.SecurityGroup{
		{
			GroupName:     aws.String("existingGroup"),
			GroupId:       aws.String("existingGroupId"),
			IpPermissions: []*ec2.IpPermission{ipPermission(testSSHPort)},
		},
	}}
	recorder.On("DescribeSecurityGroups", mock.MatchedBy(matchGroupLookup(groups))).Return(
		&initialLookupResult, nil)

	// An ingress permission is added to the existing group.
	recorder.On("AuthorizeSecurityGroupIngress", &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId:       aws.String("existingGroupId"),
		IpPermissions: []*ec2.IpPermission{ipPermission(testDockerPort)},
	}).Return(
		&ec2.AuthorizeSecurityGroupIngressOutput{}, nil)

	// The new security group is created.
	recorder.On("CreateSecurityGroup", &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String("newGroup"),
		Description: aws.String("Docker Machine"),
		VpcId:       aws.String(""),
	}).Return(
		&ec2.CreateSecurityGroupOutput{GroupId: aws.String("newGroupId")}, nil)

	// Ensuring the new security group exists.
	postCreateLookupResult := ec2.DescribeSecurityGroupsOutput{SecurityGroups: []*ec2.SecurityGroup{
		{
			GroupName: aws.String("newGroup"),
			GroupId:   aws.String("newGroupId"),
		},
	}}
	recorder.On("DescribeSecurityGroups",
		&ec2.DescribeSecurityGroupsInput{GroupIds: []*string{aws.String("newGroupId")}}).Return(
		&postCreateLookupResult, nil)

	// Permissions are added to the new security group.
	recorder.On("AuthorizeSecurityGroupIngress", &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId:       aws.String("newGroupId"),
		IpPermissions: []*ec2.IpPermission{ipPermission(testSSHPort), ipPermission(testDockerPort)},
	}).Return(
		&ec2.AuthorizeSecurityGroupIngressOutput{}, nil)

	driver := NewCustomTestDriver(&recorder)
	err := driver.configureSecurityGroups(groups)

	assert.Nil(t, err)
	recorder.AssertExpectations(t)
}

func TestConfigureSecurityGroupsErrLookupExist(t *testing.T) {
	groups := []string{"group"}
	recorder := fakeEC2SecurityGroupTestRecorder{}

	lookupExistErr := errors.New("lookup failed")
	recorder.On("DescribeSecurityGroups", mock.MatchedBy(matchGroupLookup(groups))).Return(
		nil, lookupExistErr)

	driver := NewCustomTestDriver(&recorder)
	err := driver.configureSecurityGroups(groups)

	assert.Exactly(t, lookupExistErr, err)
	recorder.AssertExpectations(t)
}

func TestBase64UserDataIsEmptyIfNoFileProvided(t *testing.T) {
	driver := NewTestDriver()

	userdata, err := driver.Base64UserData()

	assert.NoError(t, err)
	assert.Empty(t, userdata)
}

func TestBase64UserDataGeneratesErrorIfFileNotFound(t *testing.T) {
	dir, err := ioutil.TempDir("", "awsuserdata")
	assert.NoError(t, err, "Unable to create temporary directory.")

	defer os.RemoveAll(dir)
	userdata_path := filepath.Join(dir, "does-not-exist.yml")

	driver := NewTestDriver()
	driver.UserDataFile = userdata_path

	_, ud_err := driver.Base64UserData()
	assert.Equal(t, ud_err, errorReadingUserData)
}

func TestBase64UserDataIsCorrectWhenFileProvided(t *testing.T) {
	dir, err := ioutil.TempDir("", "awsuserdata")
	assert.NoError(t, err, "Unable to create temporary directory.")

	defer os.RemoveAll(dir)

	userdata_path := filepath.Join(dir, "test-userdata.yml")

	content := []byte("#cloud-config\nhostname: userdata-test\nfqdn: userdata-test.amazonec2.driver\n")
	contentBase64 := "I2Nsb3VkLWNvbmZpZwpob3N0bmFtZTogdXNlcmRhdGEtdGVzdApmcWRuOiB1c2VyZGF0YS10ZXN0LmFtYXpvbmVjMi5kcml2ZXIK"

	err = ioutil.WriteFile(userdata_path, content, 0666)
	assert.NoError(t, err, "Unable to create temporary userdata file.")

	driver := NewTestDriver()
	driver.UserDataFile = userdata_path

	userdata, ud_err := driver.Base64UserData()

	assert.NoError(t, ud_err)
	assert.Equal(t, contentBase64, userdata)
}

func TestGetIP(t *testing.T) {
	privateIPAddress := "127.0.0.1"
	publicIPAddress := "192.168.1.1"

	describeInstanceRecorder := fakeEC2DescribeInstance{}
	defer describeInstanceRecorder.AssertExpectations(t)

	describeInstanceRecorder.On("DescribeInstances", mock.Anything).
		Return(
			&ec2.DescribeInstancesOutput{
				NextToken: nil,
				Reservations: []*ec2.Reservation{
					{
						Instances: []*ec2.Instance{
							{
								PrivateIpAddress: &privateIPAddress,
								PublicIpAddress:  &publicIPAddress,
							},
						},
					},
				},
			},
			nil,
		).Once()

	driver := NewCustomTestDriver(&describeInstanceRecorder)

	// Called the first time
	ip, err := driver.GetIP()
	assert.NoError(t, err)
	assert.Equal(t, publicIPAddress, ip)

	// Set IP Address, to use cached version
	driver.IPAddress = publicIPAddress
	ip, err = driver.GetIP()
	assert.NoError(t, err)
	assert.Equal(t, publicIPAddress, ip)
}

func TestGetInstance(t *testing.T) {
	fakeInstance := &ec2.Instance{}
	fakeErr := errors.New("fake-err")

	tests := []struct {
		name                    string
		describeInstancesOutput *ec2.DescribeInstancesOutput
		err                     error
		wantInstance            *ec2.Instance
		wantErr                 error
	}{
		{
			name:                    "describe returns instances",
			describeInstancesOutput: &ec2.DescribeInstancesOutput{Reservations: []*ec2.Reservation{{Instances: []*ec2.Instance{fakeInstance}}}},
			err:                     nil,
			wantInstance:            fakeInstance,
			wantErr:                 nil,
		},
		{
			name:                    "describe returns error",
			describeInstancesOutput: nil,
			err:                     fakeErr,
			wantInstance:            nil,
			wantErr:                 fakeErr,
		},
		{
			name:                    "no describe is provided",
			describeInstancesOutput: nil,
			err:                     nil,
			wantInstance:            nil,
			wantErr:                 errorUnprocessableResponse,
		},
		{
			name:                    "describe with no reservations",
			describeInstancesOutput: &ec2.DescribeInstancesOutput{Reservations: nil},
			err:                     nil,
			wantInstance:            nil,
			wantErr:                 errorUnprocessableResponse,
		},
		{
			name:                    "describe with no instances",
			describeInstancesOutput: &ec2.DescribeInstancesOutput{Reservations: []*ec2.Reservation{{Instances: nil}}},
			err:                     nil,
			wantInstance:            nil,
			wantErr:                 errorUnprocessableResponse,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			describeInstanceRecorder := fakeEC2DescribeInstance{}
			defer describeInstanceRecorder.AssertExpectations(t)

			describeInstanceRecorder.On("DescribeInstances", mock.Anything).
				Return(tt.describeInstancesOutput, tt.err).
				Once()

			driver := NewCustomTestDriver(&describeInstanceRecorder)

			gotInstance, err := driver.getInstance()
			if tt.wantErr != nil {
				assert.Contains(t, err.Error(), tt.wantErr.Error())
			}
			assert.Equal(t, tt.wantInstance, gotInstance)
		})
	}
}
