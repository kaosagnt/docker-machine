package amazonec2

import (
	"errors"
)

type region struct {
	AmiId string
}

// Ubuntu 20.04 LTS 20240205 hvm:ebs-ssd (amd64)
// See https://cloud-images.ubuntu.com/locator/ec2/
var regionDetails = map[string]*region{
	"af-south-1":      {AmiId: "ami-0732d133808ff3999"},
	"ap-east-1":       {AmiId: "ami-063de75c5af01d92a"},
	"ap-northeast-1":  {AmiId: "ami-067983a1f071c98a2"},
	"ap-northeast-2":  {AmiId: "ami-048b1fc6c43a994cd"},
	"ap-northeast-3":  {AmiId: "ami-052dc320a1c0e058d"},
	"ap-south-1":      {AmiId: "ami-04b37139ce28e53e7"},
	"ap-south-2":      {AmiId: "ami-0c9152930c06f7813"},
	"ap-southeast-1":  {AmiId: "ami-0f64eb01ba87726eb"},
	"ap-southeast-2":  {AmiId: "ami-0bebf9059183cb33e"},
	"ap-southeast-3":  {AmiId: "ami-039527ea72d048456"},
	"ap-southeast-4":  {AmiId: "ami-0fe0895ff31a0aaa0"},
	"ca-central-1":    {AmiId: "ami-0486b4f025358dca4"},
	"ca-west-1":       {AmiId: "ami-08baa6c4e3cdd1288"},
	"cn-north-1":      {AmiId: "ami-090713a56d38daf9e"},
	"cn-northwest-1":  {AmiId: "ami-036d67d565846771b"},
	"eu-central-1":    {AmiId: "ami-00a830443b0381486"},
	"eu-central-2":    {AmiId: "ami-016a2958363bce06f"},
	"eu-north-1":      {AmiId: "ami-000b9845467bba0de"},
	"eu-south-1":      {AmiId: "ami-02be2c3a70dbe2562"},
	"eu-south-2":      {AmiId: "ami-085549792c0648fbf"},
	"eu-west-1":       {AmiId: "ami-0da9e5d20774e4bac"},
	"eu-west-2":       {AmiId: "ami-0889f6dd0117846f5"},
	"eu-west-3":       {AmiId: "ami-08d0e3f9e0d3a5fb1"},
	"il-central-1":    {AmiId: "ami-0859be55b3a3afd3a"},
	"me-central-1":    {AmiId: "ami-059080e3af8ca3792"},
	"me-south-1":      {AmiId: "ami-0770750496a7e7e8c"},
	"sa-east-1":       {AmiId: "ami-0a04072565e4bffd4"},
	"us-east-1":       {AmiId: "ami-0cadabe060becb0e1"},
	"us-east-2":       {AmiId: "ami-07b469810a61205a8"},
	"us-west-1":       {AmiId: "ami-0440a72908149722a"},
	"us-west-2":       {AmiId: "ami-05d7e58fb07229475"},
	"us-gov-east-1":   {AmiId: "ami-0eb7ef4cc0594fa04"},
	"us-gov-west-1":   {AmiId: "ami-029a634618d6c0300"},
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
