# ⚠️This is a fork of Docker Machine ⚠

This is a fork of Docker Machine maintained by GitLab for [fixing critical bugs](https://docs.gitlab.com/runner/executors/docker_machine.html#forked-version-of-docker-machine). Docker Machine, which Docker has deprecated as of 2021-09-27, is the basis of the GitLab Runner Docker Machine Executor. Our plan, as discussed [here](https://gitlab.com/gitlab-org/gitlab/-/issues/341856), is to continue to maintain the fork in the near term, with a primary focus on driver maintenance for Amazon Web Services, Google Cloud Platform, Microsoft Azure.

For a new merge request to be considered, the following questions must be answered:

  * What critical bug this MR is fixing?
  * How does this change help reduce cost of usage? What scale of cost reduction is it?
  * In what scenarios is this change usable with GitLab Runner's `docker+machine` executor?

Builds from this fork can be downloaded at https://gitlab-docker-machine-downloads.s3.amazonaws.com/main/index.html

The docs for Docker Machine are now here:
https://github.com/docker/docker.github.io/blob/173d3c65f8e7df2a8c0323594419c18086fc3a30/machine/index.md

