package main

import (
	"context"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ssm"
	"github.com/aws/aws-sdk-go/service/ssm/ssmiface"
	"github.com/google/go-github/v32/github"
	"github.com/pkg/errors"
	"golang.org/x/oauth2"
	"golang.org/x/sync/errgroup"
	"io/ioutil"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type runner struct {
	name  string
	files map[string][]byte
}

func (r *runner) dir() string {
	return "/tmp/" + r.name
}

func (r *runner) start(ctx context.Context, args ...string) error {
	err := os.MkdirAll(r.dir(), 0777)
	if err != nil {
		return errors.WithStack(err)
	}

	for name, contents := range r.files {
		err = ioutil.WriteFile(filepath.Join(r.dir(), name), contents, 0700)
		if err != nil {
			return errors.WithStack(err)
		}
	}

	cmd := exec.CommandContext(ctx, "/runner/bin/Runner.Listener", args...)
	cmd.Env = append(os.Environ(), "CBA_PATH_SUBSTITUTION="+r.dir(), "LD_PRELOAD=/runner/preload.so")
	cmd.Dir = "/runner/bin"
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Start()
	if err != nil {
		return errors.WithStack(err)
	}

	/*
		TODO respond to these exit codes

			// Return code definition: (this will be used by service host to determine whether it will re-launch Runner.Listener)
			// 0: Runner exit
			// 1: Terminate failure
			// 2: Retriable failure
			// 3: Exit for self update
	*/

	err = waitOrStop(ctx, cmd, 10*time.Second)
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func main() {
	ctx := context.Background()
	rand.Seed(time.Now().UTC().UnixNano())

	sess, err := session.NewSession()
	if err != nil {
		panic(err)
	}

	api := ssm.New(sess)

	resp, err := api.GetParametersWithContext(ctx, &ssm.GetParametersInput{
		WithDecryption: aws.Bool(true),
		Names:          aws.StringSlice([]string{"/cbactions/token", "/cbactions/owner"}),
	})
	if err != nil {
		panic(err)
	}

	token := ""
	owner := ""
	for _, param := range resp.Parameters {
		switch *param.Name {
		case "/cbactions/token":
			token = *param.Value
		case "/cbactions/owner":
			owner = *param.Value
		}
	}

	_, projectFound := os.LookupEnv("CODEBUILD_PROJECT_NAME")
	_, kmsKeyFound := os.LookupEnv("KMS_KEY_ARN")


	if token == "" || owner == "" || !projectFound || !kmsKeyFound {
		panic("you need to set /cbactions/token and /cbactions/owner in parameter store and provide CODEBUILD_PROJECT_NAME and KMS_KEY_ARN env vars")
	}

	//err = removeAllRunners(ctx, token, owner)
	//if err != nil {
	//	panic(err)
	//}
	//
	//os.Exit(0)

	runners, err := getRunnerConfigs(ctx, api)
	if err != nil {
		panic(err)
	}

	if len(runners) < 5 {
		for idx := 0; idx < 5; idx++ {
			err = registerRunner(ctx, api, token, owner)
			if err != nil {
				panic(err)
			}
		}

		runners, err = getRunnerConfigs(ctx, api)
		if err != nil {
			panic(err)
		}
	}

	g, ctx := errgroup.WithContext(ctx)

	for _, r := range runners {
		r := r
		g.Go(func() error {
			return r.start(ctx, "run")
		})
	}

	err = g.Wait()
	if err != nil {
		panic(err)
	}
}

func getRunnerConfigs(ctx context.Context, api ssmiface.SSMAPI) (map[string]*runner, error) {
	params := []*ssm.Parameter{}

	err := api.GetParametersByPathPagesWithContext(ctx, &ssm.GetParametersByPathInput{
		Path:           aws.String("/cbactions/runners"),
		Recursive:      aws.Bool(true),
		WithDecryption: aws.Bool(true),
	}, func(page *ssm.GetParametersByPathOutput, lastPage bool) bool {
		params = append(params, page.Parameters...)
		return !lastPage
	})
	if err != nil {
		return nil, errors.WithStack(err)
	}

	runners := map[string]*runner{}

	for _, param := range params {
		parts := strings.SplitN(*param.Name, "/", 5)
		name := parts[3]
		file := parts[4]

		r := runners[name]
		if r == nil {
			r = &runner{
				name:  name,
				files: map[string][]byte{},
			}
			runners[name] = r
		}

		r.files[file] = []byte(*param.Value)
	}

	return runners, nil
}

func registerRunner(ctx context.Context, api ssmiface.SSMAPI, accessToken, owner string) error {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: accessToken})
	tc := oauth2.NewClient(ctx, ts)
	gh := github.NewClient(tc)

	token, _, err := gh.Actions.CreateOrganizationRegistrationToken(ctx, owner)
	if err != nil {
		return errors.WithStack(err)
	}

	url := "https://github.com/" + owner
	name := fmt.Sprintf("cbactions%d", rand.Intn(10e3))

	runner := &runner{
		name:  name,
		files: map[string][]byte{},
	}

	err = runner.start(ctx, "configure", "--unattended", "--name", name, "--url", url, "--token", *token.Token)
	if err != nil {
		return errors.WithStack(err)
	}

	dir := runner.dir()

	files := []string{".credentials", ".credentials_rsaparams", ".runner"}
	for _, file := range files {
		contents, err := ioutil.ReadFile(filepath.Join(dir, file))
		if err != nil {
			return errors.WithStack(err)
		}

		_, err = api.PutParameterWithContext(ctx, &ssm.PutParameterInput{
			Name:  aws.String(fmt.Sprintf("/cbactions/runners/%s/%s", name, file)),
			Type:  aws.String(ssm.ParameterTypeSecureString),
			Value: aws.String(string(contents)),
		})
		if err != nil {
			return errors.WithStack(err)
		}
	}

	return nil
}

func removeAllRunners(ctx context.Context, accessToken, owner string) error {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: accessToken})
	tc := oauth2.NewClient(ctx, ts)
	gh := github.NewClient(tc)

	opts := &github.ListOptions{}
	runners := []*github.Runner{}

	for {
		page, resp, err := gh.Actions.ListOrganizationRunners(ctx, owner, opts)
		if err != nil {
			return errors.WithStack(err)
		}

		runners = append(runners, page.Runners...)
		if resp.NextPage == 0 {
			break
		}

		opts.Page = resp.NextPage
	}

	for _, r := range runners {
		fmt.Printf("removing %s\n", r.GetName())
		_, err := gh.Actions.RemoveOrganizationRunner(ctx, owner, r.GetID())
		if err != nil {
			return errors.WithStack(err)
		}
	}

	return nil
}
