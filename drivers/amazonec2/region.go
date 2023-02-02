package amazonec2

import (
	"errors"
)

type region struct {
	AmiId string
}

// Ubuntu 20.04 LTS 20220420 hvm:ebs-ssd (amd64)
// See https://cloud-images.ubuntu.com/locator/ec2/
var regionDetails = map[string]*region{
	"af-south-1":      {AmiId: "ami-0670428c515903d37"},
	"ap-east-1":       {AmiId: "ami-0350928fdb53ae439"},
	"ap-northeast-1":  {AmiId: "ami-0a3eb6ca097b78895"},
	"ap-northeast-2":  {AmiId: "ami-0225bc2990c54ce9a"},
	"ap-northeast-3":  {AmiId: "ami-0c2223049202ca738"},
	"ap-south-1":      {AmiId: "ami-05ba3a39a75be1ec4"},
	"ap-south-2":      {AmiId: "ami-0cdec4d7db18a5cdb"},
	"ap-southeast-1":  {AmiId: "ami-0750a20e9959e44ff"},
	"ap-southeast-2":  {AmiId: "ami-0d539270873f66397"},
	"ap-southeast-3":  {AmiId: "ami-0f06496957d1fe04a"},
	"ca-central-1":    {AmiId: "ami-073c944d45ffb4f27"},
	"cn-north-1":      {AmiId: "ami-0741e7b8b4fb0001c"},
	"cn-northwest-1":  {AmiId: "ami-0883e8062ff31f727"},
	"eu-central-1":    {AmiId: "ami-02584c1c9d05efa69"},
	"eu-central-2":    {AmiId: "ami-0968892c976bc03f2"},
	"eu-north-1":      {AmiId: "ami-09f0506c9ef0fb473"},
	"eu-south-1":      {AmiId: "ami-06ea0ad3f5adc2565"},
	"eu-south-2":      {AmiId: "ami-0d3d6b90b90290cdd"},
	"eu-west-1":       {AmiId: "ami-00e7df8df28dfa791"},
	"eu-west-2":       {AmiId: "ami-00826bd51e68b1487"},
	"eu-west-3":       {AmiId: "ami-0a21d1c76ac56fee7"},
	"me-central-1":    {AmiId: "ami-04e59379df0314070"},
	"me-south-1":      {AmiId: "ami-05b680b37c7917206"},
	"sa-east-1":       {AmiId: "ami-077518a464c82703b"},
	"us-east-1":       {AmiId: "ami-0c4f7023847b90238"},
	"us-east-2":       {AmiId: "ami-0eea504f45ef7a8f7"},
	"us-gov-east-1":   {AmiId: "ami-0eb7ef4cc0594fa04"},
	"us-gov-west-1":   {AmiId: "ami-029a634618d6c0300"},
	"us-west-1":       {AmiId: "ami-0487b1fe60c1fd1a2"},
	"us-west-2":       {AmiId: "ami-0cb4e786f15603b0d"},
	"custom-endpoint": {""},
}

func awsRegionsList() []string {
	var list []string

	for k := range regionDetails {
		list = append(list, k)
	}

	return list
}

func validateAwsRegion(region string) (string, error) {
	for _, v := range awsRegionsList() {
		if v == region {
			return region, nil
		}
	}

	return "", errors.New("Invalid region specified")
}
