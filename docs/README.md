# Run your GitHub Actions in AWS CodeBuild

You can have have all the benefits of GitHub Actions:

* A broad range of events to trigger workflows
* A rich system to express event conditions for running workflows
* A nice way to compose workflows using "actions"
* A great UX with deep GitHub integration

**And** the benefits of AWS CodeBuild:

* Up to 72 vCPUs, 255GB of RAM and GPU options
* AWS VPC network connectivity
* AWS IAM roles

## Setup instructions

TODO

## How it works

Forewarning: it's not pretty. There are at least three processes in play.

`mux`: This is the entrypoint for the long-running Fargate task. This launches
multiple (default five) `Runner.Listener` processes. Each one of these processes
has unique runner-specific credentials and hence appears as a separate runner
in GitHub Actions. This is achieved using `LD_PRELOAD=/runner/preload.so`.

`Runner.Listener`: This is the long-running process in the self-hosted runner.
It polls GitHub Actions for new jobs to run. Each runner can only process one
job at a time, hence there are multiple of this process running.

`worker`: When `Runner.Listener` receives a job, it launches `Runner.Worker`
and sends the job information over a pipe to the child process. The GitHub
`Runner.Worker` is overwritten in the Fargate image with our `worker`. Our
process encrypts the job information and starts a new build in CodeBuild with
the encrypted job as an environment variable. It then polls CodeBuild waiting
for the build to complete.

`entrypoint`: This is the only program executed by CodeBuild. It decrypts the
encrypted job information and then launches `Runner.Worker` and sends it the job
information over a pipe. It then exports `Runner.Worker`'s exit code so that
`worker` can return it to `Runner.Listener`.

`preload`: This is a shared library injected into `Runner.Listener` that intercepts
calls to libc's `open`, `__xstat64` and `__lxstat64`. This is because GitHub's
`Runner.Listener` expects the runner's credentials to be at a hard-coded path
and our shared library can instead return the contents of `/tmp/<something>/.credentials`
when `/runner/.credentials` is requested. That's how we achieve multiple runners
in a single container. 

![diagram of processes](/docs/cbactions.png)

## What about [`aws-actions/aws-codebuild-run-build`][aws-action]?

So your first question is _how is this different to the CodeBuild action? That 
lets me run CodeBuild from Actions._ Good question. That action triggers a 
CodeBuild  build _from_ a GitHub action, so you end up paying for both. It also 
means that  you need to store AWS credentials in GitHub - which necessitates an 
IAM user.

This project is different in that it uses GitHub's support for "self-hosted" 
runners to run your actions _in_ CodeBuild. This way you only pay for CodeBuild,
but you still get the benefits of the great GitHub Actions UX.  You also don't 
need to write a buildspec _and_ an action workflow.

[aws-action]: https://github.com/aws-actions/aws-codebuild-run-build
