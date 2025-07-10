package amazonec2

import (
	"errors"
)

type region struct {
	AmiId string
}

// Ubuntu 20.04 LTS 20250624 hvm:ebs-ssd (amd64)
// See https://cloud-images.ubuntu.com/locator/ec2/
var regionDetails = map[string]*region{
	"af-south-1":      {AmiId: "ami-08dc1096b47733f08"},
	"ap-east-1":       {AmiId: "ami-0ce9159196fa630f0"},
	"ap-east-2":       {AmiId: "ami-065c8be12b0c71019"},
	"ap-northeast-1":  {AmiId: "ami-0836e97b3d843dd82"},
	"ap-northeast-2":  {AmiId: "ami-0f8d552e06067b477"},
	"ap-northeast-3":  {AmiId: "ami-03ab230a152fe777b"},
	"ap-south-1":      {AmiId: "ami-06cc5ebfb8571a147"},
	"ap-south-2":      {AmiId: "ami-0c021d3a56971d13a"},
	"ap-southeast-1":  {AmiId: "ami-08b138b7cf65145b1"},
	"ap-southeast-2":  {AmiId: "ami-06cd25c905a7e9a0f"},
	"ap-southeast-3":  {AmiId: "ami-07b99ebf9e44f72bc"},
	"ap-southeast-4":  {AmiId: "ami-0cf5279adbb219ec9"},
	"ap-southeast-5":  {AmiId: "ami-0fc5fa8f3d2860a8f"},
	"ap-southeast-7":  {AmiId: "ami-065dc5b24996c2a07"},
	"ca-central-1":    {AmiId: "ami-0495a93f1f3b4a562"},
	"ca-west-1":       {AmiId: "ami-01b9f61899f9bb1fa"},
	"cn-north-1":      {AmiId: "ami-05f5d493e2cf7b97a"},
	"cn-northwest-1":  {AmiId: "ami-08ed51467ba8bad52"},
	"eu-central-1":    {AmiId: "ami-0cebfb1f908092578"},
	"eu-central-2":    {AmiId: "ami-033d9c6fcfad7c42e"},
	"eu-north-1":      {AmiId: "ami-01637463b2cbe7cb6"},
	"eu-south-1":      {AmiId: "ami-05e7ecda2dd8dcb7a"},
	"eu-south-2":      {AmiId: "ami-013c7686735b77f96"},
	"eu-west-1":       {AmiId: "ami-0910be1e1d214d762"},
	"eu-west-2":       {AmiId: "ami-0f321d890424a6390"},
	"eu-west-3":       {AmiId: "ami-02ab616bef07ac291"},
	"il-central-1":    {AmiId: "ami-0d31a417c2e969eb4"},
	"me-central-1":    {AmiId: "ami-06abd59faf3628aaf"},
	"me-south-1":      {AmiId: "ami-0bcb89ed2c3d0e585"},
	"mx-central-1":    {AmiId: "ami-01775358d07d09cb0"},
	"sa-east-1":       {AmiId: "ami-066489b5435ba6817"},
	"us-east-1":       {AmiId: "ami-0fb0b230890ccd1e6"},
	"us-east-2":       {AmiId: "ami-076838d6a293cb49e"},
	"us-west-1":       {AmiId: "ami-08fe9bdd11670668e"},
	"us-west-2":       {AmiId: "ami-0a15226b1f7f23580"},
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
