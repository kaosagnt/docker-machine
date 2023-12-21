#!/usr/bin/env bats

load ${BASE_TEST_DIR}/helpers.bash

only_if_env DRIVER amazonec2

use_disposable_machine

require_env AWS_ACCESS_KEY_ID
require_env AWS_SECRET_ACCESS_KEY
require_env AWS_VPC_ID

require_env AWS_DEFAULT_REGION
require_env AWS_ZONE

@test "$DRIVER: Should Create an EBS Encrypted Instance" {
    #Use Instance Type that supports EBS Optimize
    run machine create -d amazonec2 --amazonec2-instance-type=t2.micro --amazonec2-volume-encrypted $NAME
    echo ${output}
    [ "$status" -eq 0 ]
}

@test "$DRIVER: Check the machine is up and EBS is encrypted" {
    # Get instance id
    run docker $(machine config $NAME) run --rm -e AWS_ACCESS_KEY_ID=$AWS_ACCESS_KEY_ID -e AWS_SECRET_ACCESS_KEY=$AWS_SECRET_ACCESS_KEY -e AWS_DEFAULT_REGION=$AWS_DEFAULT_REGION -e AWS_ZONE=$AWS_ZONE -e AWS_VPC_ID=$AWS_VPC_ID amazon/aws-cli ec2 describe-instances --filters Name=tag:Name,Values=$NAME Name=instance-state-name,Values=running --query 'Reservations[0].Instances[0].InstanceId' --output text
    instance_id=${lines[-1]}
    # verify if volume attached is encrypted
    run docker $(machine config $NAME) run --rm -e AWS_ACCESS_KEY_ID=$AWS_ACCESS_KEY_ID -e AWS_SECRET_ACCESS_KEY=$AWS_SECRET_ACCESS_KEY -e AWS_DEFAULT_REGION=$AWS_DEFAULT_REGION -e AWS_ZONE=$AWS_ZONE -e AWS_VPC_ID=$AWS_VPC_ID amazon/aws-cli ec2 describe-volumes --filters Name=attachment.instance-id,Values="${instance_id}" --query 'Volumes[0].Encrypted' --output text
    encrypted=${lines[-1]}
    [[ $encrypted = "True" ]]
}
