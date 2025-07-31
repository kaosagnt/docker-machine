package amazonec2

import (
	"errors"
)

type region struct {
	AmiId string
}

// Ubuntu 22.04 LTS 20250712 hvm:ebs-ssd (amd64)
// See https://cloud-images.ubuntu.com/locator/ec2/
var regionDetails = map[string]*region{
	"af-south-1":      {AmiId: "ami-041bfdb89f7e6ba0d"},
	"ap-east-1":       {AmiId: "ami-007b5f22907161c68"},
	"ap-east-2":       {AmiId: "ami-0dc6e177da48882c4"},
	"ap-northeast-1":  {AmiId: "ami-0c48fa60af31d0d5b"},
	"ap-northeast-2":  {AmiId: "ami-01f71f215b23ba262"},
	"ap-northeast-3":  {AmiId: "ami-0f0aec92c8dcb3291"},
	"ap-south-1":      {AmiId: "ami-06a644026f43160a5"},
	"ap-south-2":      {AmiId: "ami-0ae3c7e4a9995a99e"},
	"ap-southeast-1":  {AmiId: "ami-04056160f0fcaaf17"},
	"ap-southeast-2":  {AmiId: "ami-014a86597c022868e"},
	"ap-southeast-3":  {AmiId: "ami-0e6f15d74a4ac5f6d"},
	"ap-southeast-4":  {AmiId: "ami-080cd61aa0dbd1e31"},
	"ap-southeast-5":  {AmiId: "ami-087005820c0c36cdc"},
	"ap-southeast-7":  {AmiId: "ami-019d987fb8e446d1a"},
	"ca-central-1":    {AmiId: "ami-09ac47f9dcb88f998"},
	"ca-west-1":       {AmiId: "ami-0e22b16ac21cf80da"},
	"cn-north-1":      {AmiId: "ami-00cb6422cf29840d4"},
	"cn-northwest-1":  {AmiId: "ami-0744d8adb00794f00"},
	"eu-central-1":    {AmiId: "ami-0dc33c9c954b3f073"},
	"eu-central-2":    {AmiId: "ami-02c8aa9c4d9411cff"},
	"eu-north-1":      {AmiId: "ami-0b8e4d801c75b0f0d"},
	"eu-south-1":      {AmiId: "ami-003d6c9c7b472a007"},
	"eu-south-2":      {AmiId: "ami-0bca4f1b43fd22ef0"},
	"eu-west-1":       {AmiId: "ami-064673ca419016c37"},
	"eu-west-2":       {AmiId: "ami-08f79bee58074adeb"},
	"eu-west-3":       {AmiId: "ami-0309b5fc16a20deb4"},
	"il-central-1":    {AmiId: "ami-0fb4a62f4a91e1809"},
	"me-central-1":    {AmiId: "ami-088c100a22a7e8cf8"},
	"me-south-1":      {AmiId: "ami-08f6d0a0bb4c339d0"},
	"mx-central-1":    {AmiId: "ami-07767eae6e438d32a"},
	"sa-east-1":       {AmiId: "ami-0d6d5b74032865309"},
	"us-east-1":       {AmiId: "ami-09ac0b140f63d3458"},
	"us-east-2":       {AmiId: "ami-05eb56e0befdb025f"},
	"us-west-1":       {AmiId: "ami-046070fb756e4377e"},
	"us-west-2":       {AmiId: "ami-0597e0308dc02ed24"},
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
