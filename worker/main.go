package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"filippo.io/age"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/codebuild"
	"github.com/aws/aws-sdk-go/service/kms"
	"github.com/glassechidna/cbactions"
	"github.com/pkg/errors"
	"io"
	"io/ioutil"
	"os"
	"strconv"
	"time"
	"unicode/utf16"
)

func main() {
	inNum, _ := strconv.Atoi(os.Args[2])
	in := os.NewFile(uintptr(inNum), "ghin")

	msg, err := drainPipe(in)
	if err != nil {
		panic(err)
	}

	identity, err := age.GenerateX25519Identity()
	if err != nil {
		panic(err)
	}

	payload, err := encrypt(identity, []byte(msg))
	if err != nil {
		panic(err)
	}

	files, err := bundleConfigFiles(identity)
	if err != nil {
		panic(err)
	}

	sess, err := session.NewSession()
	if err != nil {
		panic(err)
	}

	encKey, err := encryptKey(sess, identity.String())
	if err != nil {
		panic(err)
	}

	cbapi := codebuild.New(sess)
	cresp, err := cbapi.StartBuild(&codebuild.StartBuildInput{
		ProjectName: aws.String(os.Getenv("CODEBUILD_PROJECT_NAME")),
		EnvironmentVariablesOverride: []*codebuild.EnvironmentVariable{
			{
				Name:  aws.String("CBA_KEY"),
				Type:  aws.String(codebuild.EnvironmentVariableTypePlaintext),
				Value: &encKey,
			},
			{
				Name:  aws.String("CBA_PAYLOAD"),
				Type:  aws.String(codebuild.EnvironmentVariableTypePlaintext),
				Value: &payload,
			},
			{
				Name:  aws.String("CBA_FILES"),
				Type:  aws.String(codebuild.EnvironmentVariableTypePlaintext),
				Value: &files,
			},
		},
	})
	if err != nil {
		panic(err)
	}

	build, err := waitForBuild(cbapi, *cresp.Build.Arn)
	if err != nil {
		panic(err)
	}

	envvar := build.ExportedEnvironmentVariables[0]
	if *envvar.Name != "RUNNER_EXITCODE" {
		panic("unexpected exported env var")
	}

	exitCode, _ := strconv.Atoi(*envvar.Value)
	if exitCode == 0 {
		panic("unexpected exit code")
	}

	os.Exit(exitCode)
}

func encryptKey(sess *session.Session, rawKey string) (string, error) {
	kapi := kms.New(sess)

	kresp, err := kapi.Encrypt(&kms.EncryptInput{
		KeyId:     aws.String(os.Getenv("KMS_KEY_ARN")),
		Plaintext: []byte(rawKey),
	})
	if err != nil {
		return "", errors.WithStack(err)
	}

	encKey := base64.StdEncoding.EncodeToString(kresp.CiphertextBlob)
	return encKey, nil
}

func drainPipe(in *os.File) ([]byte, error) {
	var msgType int32
	err := binary.Read(in, binary.LittleEndian, &msgType)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	var strlen int32
	err = binary.Read(in, binary.LittleEndian, &strlen)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	msgbuf := make([]byte, strlen)
	_, err = io.ReadFull(in, msgbuf)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	msg, err := decodeUtf16(msgbuf, binary.LittleEndian)
	if err != nil {
		return nil, err
	}

	return []byte(msg), nil
}

func bundleConfigFiles(identity *age.X25519Identity) (string, error) {
	paths := []string{"/runner/.credentials", "/runner/.credentials_rsaparams", "/runner/.runner"}
	m := map[string][]byte{}

	for _, path := range paths {
		path = cbactions.RewritePath(path)
		content, err := ioutil.ReadFile(path)
		if err != nil {
			return "", errors.WithStack(err)
		}

		m[path] = content
	}

	filesJson, _ := json.Marshal(m)
	return encrypt(identity, filesJson)
}

func encrypt(identity *age.X25519Identity, msg []byte) (string, error) {
	out := &bytes.Buffer{}
	w, err := age.Encrypt(out, identity.Recipient())
	if err != nil {
		return "", errors.WithStack(err)
	}

	_, err = w.Write(msg)
	if err != nil {
		return "", errors.WithStack(err)
	}

	err = w.Close()
	if err != nil {
		return "", errors.WithStack(err)
	}

	payload := base64.StdEncoding.EncodeToString(out.Bytes())
	return payload, nil
}

func waitForBuild(cbapi *codebuild.CodeBuild, arn string) (*codebuild.Build, error) {
	start := time.Now()

	for time.Now().Sub(start) < time.Hour {
		bresp, err := cbapi.BatchGetBuilds(&codebuild.BatchGetBuildsInput{Ids: []*string{&arn}})
		if err != nil {
			panic(err)
		}

		build := bresp.Builds[0]
		if *build.BuildStatus != codebuild.StatusTypeInProgress {
			return build, nil
		}

		time.Sleep(10 * time.Second)
	}

	return nil, errors.New("never found")
}

func decodeUtf16(b []byte, order binary.ByteOrder) (string, error) {
	r := bytes.NewReader(b)

	ints := make([]uint16, len(b)/2)
	err := binary.Read(r, order, &ints)
	if err != nil {
		return "", errors.WithStack(err)
	}

	return string(utf16.Decode(ints)), nil
}
