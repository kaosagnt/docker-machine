<!--[metadata]>
+++
title = "Amazon Web Services"
description = "Amazon Web Services driver for machine"
keywords = ["machine, Amazon Web Services, driver"]
[menu.main]
parent="smn_machine_drivers"
+++
<![end-metadata]-->

# Amazon Web Services

Create machines on [Amazon Web Services](http://aws.amazon.com).

To create machines on [Amazon Web Services](http://aws.amazon.com), you must supply two parameters: the AWS Access Key ID and the AWS Secret Access Key.

## Configuring credentials

Before using the amazonec2 driver, ensure that you've configured credentials.

### AWS credential file

One way to configure credentials is to use the standard credential file for Amazon AWS `~/.aws/credentials` file, which might look like:

    [default]
    aws_access_key_id = AKID1234567890
    aws_secret_access_key = MY-SECRET-KEY

On Mac OS or various flavors of Linux you can install the [AWS Command Line Interface](http://docs.aws.amazon.com/cli/latest/userguide/cli-chap-getting-started.html#cli-quick-configuration) (`aws cli`) in the terminal and use the `aws configure` command which guides you through the creation of the credentials file.

This is the simplest method, you can then create a new machine with:

    $ docker-machine create --driver amazonec2 aws01

### Command line flags

Alternatively, you can use the flags `--amazonec2-access-key` and `--amazonec2-secret-key` on the command line:

    $ docker-machine create --driver amazonec2 --amazonec2-access-key AKI******* --amazonec2-secret-key 8T93C*******  aws01

### Environment variables

You can use environment variables:

    $ export AWS_ACCESS_KEY_ID=AKID1234567890
    $ export AWS_SECRET_ACCESS_KEY=MY-SECRET-KEY
    $ docker-machine create --driver amazonec2 aws01

## Options

-   `--amazonec2-access-key`: Your access key id for the Amazon Web Services API.
-   `--amazonec2-secret-key`: Your secret access key for the Amazon Web Services API.
-   `--amazonec2-session-token`: Your session token for the Amazon Web Services API.
-   `--amazonec2-ami`: The AMI ID of the instance to use.
-   `--amazonec2-region`: The region to use when launching the instance.
-   `--amazonec2-vpc-id`: Your VPC ID to launch the instance in.
-   `--amazonec2-zone`: The AWS zone to launch the instance in (i.e. one of a,b,c,d,e).
-   `--amazonec2-subnet-id`: AWS VPC subnet id.
-   `--amazonec2-security-group`: AWS VPC security group name.
-   `--amazonec2-tags`: AWS extra tag key-value pairs (comma-separated, e.g. key1,value1,key2,value2).
-   `--amazonec2-instance-type`: The instance type to run.
-   `--amazonec2-device-name`: The root device name of the instance.
-   `--amazonec2-root-size`: The root disk size of the instance (in GB).
-   `--amazonec2-volume-type`: The Amazon EBS volume type to be attached to the instance.
-   `--amazonec2-volume-encrypted`: Encrypt Amazon EBS volume attached to the instance.
-   `--amazonec2-volume-kms-key`: The KMS Key ID/ARN/Alias to be used to encrypt the volume.
-   `--amazonec2-iam-instance-profile`: The AWS IAM role name to be used as the instance profile.
-   `--amazonec2-ssh-user`: The SSH Login username, which must match the default SSH user set in the ami used.
-   `--amazonec2-request-spot-instance`: Use spot instances.
-   `--amazonec2-spot-price`: Spot instance bid price (in dollars). Require the `--amazonec2-request-spot-instance` flag.
-   `--amazonec2-use-private-address`: Use the private IP address for docker-machine, but still create a public IP address.
-   `--amazonec2-private-address-only`: Use the private IP address only.
-   `--amazonec2-monitoring`: Enable CloudWatch Monitoring.
-   `--amazonec2-use-ebs-optimized-instance`: Create an EBS Optimized Instance, instance type must support it.
-   `--amazonec2-ssh-keypath`: Path to Private Key file to use for instance. Matching public key with .pub extension should exist
-   `--amazonec2-retries`:  Set retry count for recoverable failures (use -1 to disable)


#### Environment variables and default values:

| CLI option                               | Environment variable    | Default          |
| ---------------------------------------- | ----------------------- | ---------------- |
| `--amazonec2-access-key`                 | `AWS_ACCESS_KEY_ID`     | -                |
| `--amazonec2-secret-key`                 | `AWS_SECRET_ACCESS_KEY` | -                |
| `--amazonec2-session-token`              | `AWS_SESSION_TOKEN`     | -                |
| `--amazonec2-ami`                        | `AWS_AMI`               | `ami-5f709f34`   |
| `--amazonec2-region`                     | `AWS_DEFAULT_REGION`    | `us-east-1`      |
| `--amazonec2-vpc-id`                     | `AWS_VPC_ID`            | -                |
| `--amazonec2-zone`                       | `AWS_ZONE`              | `a`              |
| `--amazonec2-subnet-id`                  | `AWS_SUBNET_ID`         | -                |
| `--amazonec2-security-group`             | `AWS_SECURITY_GROUP`    | `docker-machine` |
| `--amazonec2-tags`                       | `AWS_TAGS`              | -                |
| `--amazonec2-instance-type`              | `AWS_INSTANCE_TYPE`     | `t2.micro`       |
| `--amazonec2-device-name`                | `AWS_DEVICE_NAME`       | `/dev/sda1`      |
| `--amazonec2-root-size`                  | `AWS_ROOT_SIZE`         | `16`             |
| `--amazonec2-volume-type`                | `AWS_VOLUME_TYPE`       | `gp2`            |
| `--amazonec2-volume-encrypted`           | -                       | `false`          |
| `--amazonec2-volume-kms-key`             | `AWS_VOLUME_KMS_KEY`    | -                |
| `--amazonec2-iam-instance-profile`       | `AWS_INSTANCE_PROFILE`  | -                |
| `--amazonec2-ssh-user`                   | `AWS_SSH_USER`          | `ubuntu`         |
| `--amazonec2-request-spot-instance`      | -                       | `false`          |
| `--amazonec2-spot-price`                 | -                       | `0.50`           |
| `--amazonec2-use-private-address`        | -                       | `false`          |
| `--amazonec2-private-address-only`       | -                       | `false`          |
| `--amazonec2-monitoring`                 | -                       | `false`          |
| `--amazonec2-use-ebs-optimized-instance` | -                       | `false`          |
| `--amazonec2-ssh-keypath`                | `AWS_SSH_KEYPATH`       | -                |
| `--amazonec2-retries`                    | -                       | `5`              |

## Default AMIs

By default, the Amazon EC2 driver will use the Ubuntu 20.04 image for the given region:

| Region         | AMI ID       |
| -------------- | ------------ |
| af-south-1 | ami-0670428c515903d37 |
| ap-east-1 | ami-0350928fdb53ae439 |
| ap-northeast-1 | ami-0a3eb6ca097b78895 |
| ap-northeast-2 | ami-0225bc2990c54ce9a |
| ap-northeast-3 | ami-0c2223049202ca738 |
| ap-south-1 | ami-05ba3a39a75be1ec4 |
| ap-south-2 | ami-0cdec4d7db18a5cdb |
| ap-southeast-1 | ami-0750a20e9959e44ff |
| ap-southeast-2 | ami-0d539270873f66397 |
| ap-southeast-3 | ami-0f06496957d1fe04a |
| ca-central-1 | ami-073c944d45ffb4f27 |
| cn-north-1 | ami-0741e7b8b4fb0001c |
| cn-northwest-1 | ami-0883e8062ff31f727 |
| eu-central-1 | ami-02584c1c9d05efa69 |
| eu-central-2 | ami-0968892c976bc03f2 |
| eu-north-1 | ami-09f0506c9ef0fb473 |
| eu-south-1 | ami-06ea0ad3f5adc2565 |
| eu-south-2 | ami-0d3d6b90b90290cdd |
| eu-west-1 | ami-00e7df8df28dfa791 |
| eu-west-2 | ami-00826bd51e68b1487 |
| eu-west-3 | ami-0a21d1c76ac56fee7 |
| me-central-1 | ami-04e59379df0314070 |
| me-south-1 | ami-05b680b37c7917206 |
| sa-east-1 | ami-077518a464c82703b |
| us-east-1 | ami-0c4f7023847b90238 |
| us-east-2 | ami-0eea504f45ef7a8f7 |
| us-gov-east-1 | ami-0eb7ef4cc0594fa04 |
| us-gov-west-1 | ami-029a634618d6c0300 |
| us-west-1 | ami-0487b1fe60c1fd1a2 |
| us-west-2 | ami-0cb4e786f15603b0d |

## Security Group

Note that a security group will be created and associated to the host. This security group will have the following ports opened inbound:

-   ssh (22/tcp)
-   docker (2376/tcp)
-   swarm (3376/tcp), only if the node is a swarm master

If you specify a security group yourself using the `--amazonec2-security-group` flag, the above ports will be checked and opened and the security group modified.
If you want more ports to be opened, like application specific ports, use the aws console and modify the configuration manually.

## VPC ID

We determine your default vpc id at the start of a command.
In some cases, either because your account does not have a default vpc, or you don't want to use the default one, you can specify a vpc with the `--amazonec2-vpc-id` flag.

To find the VPC ID:

1.  Login to the AWS console
2.  Go to **Services -> VPC -> Your VPCs**.
3.  Locate the VPC ID you want from the _VPC_ column.
4.  Go to **Services -> VPC -> Subnets**. Examine the _Availability Zone_ column to verify that zone `a` exists and matches your VPC ID.

    For example, `us-east1-a` is in the `a` availability zone. If the `a` zone is not present, you can create a new subnet in that zone or specify a different zone when you create the machine.

To create a machine with a non-default vpc-id:

    $ docker-machine create --driver amazonec2 --amazonec2-access-key AKI******* --amazonec2-secret-key 8T93C********* --amazonec2-vpc-id vpc-****** aws02

This example assumes the VPC ID was found in the `a` availability zone. Use the`--amazonec2-zone` flag to specify a zone other than the `a` zone. For example, `--amazonec2-zone c` signifies `us-east1-c`.

## VPC Connectivity
Machine uses SSH to complete the set up of instances in EC2 and requires the ability to access the instance directly.  

If you use the flag `--amazonec2-private-address-only`, you will need to ensure that you have some method of accessing the new instance from within the internal network of the VPC (e.g. a corporate VPN to the VPC, a VPN instance inside the VPC or using Docker-machine from an instance within your VPC). 

Configuration of VPCs is beyond the scope of this guide, however the first step in troubleshooting is ensuring if you are using private subnets that you follow the design guidance in the [AWS VPC User Guide](http://docs.aws.amazon.com/AmazonVPC/latest/UserGuide/VPC_Scenario2.html) and have some form of NAT available so that the set up process can access the internet to complete set up.

## Custom AMI and SSH username

The default SSH username for the default AMIs is `ubuntu`.

You need to change the SSH username only if the custom AMI you use has a different SSH username.

You can change the SSH username with the `--amazonec2-ssh-user` according to the AMI you selected with the `--amazonec2-ami`.
