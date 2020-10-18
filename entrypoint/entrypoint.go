package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"filippo.io/age"
	"fmt"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/kms"
	"github.com/pkg/errors"
	"golang.org/x/text/encoding/unicode"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	sess, err := session.NewSession()
	if err != nil {
		panic(err)
	}

	kapi := kms.New(sess)

	identities, err := decryptorIdentities(popEnv("CBA_KEY"), kapi)
	if err != nil {
		panic(err)
	}

	payload, err := decrypt(popEnv("CBA_PAYLOAD"), identities)
	if err != nil {
		panic(err)
	}

	err = writeFiles(popEnv("CBA_FILES"), identities)
	if err != nil {
		panic(err)
	}

	exitCode, err := executeWorker(payload)
	if err != nil {
		panic(err)
	}

	err = ioutil.WriteFile("/tmp/cbactions_exitcode.txt", []byte(fmt.Sprintf("%d", exitCode)), 0600)
	if err != nil {
		panic(err)
	}
}

func writeFiles(ciphertext string, identities []age.Identity) error {
	plaintext, err := decrypt(ciphertext, identities)
	if err != nil {
		return err
	}

	m := map[string][]byte{}
	err = json.Unmarshal(plaintext, &m)
	if err != nil {
		return errors.WithStack(err)
	}

	for filename, content := range m {
		err = ioutil.WriteFile(filepath.Join("/runner", filename), content, 0600)
		if err != nil {
			return errors.WithStack(err)
		}
	}

	return nil
}

func executeWorker(payload []byte) (int, error) {
	rin, win, err := os.Pipe()
	if err != nil {
		return -1, errors.WithStack(err)
	}

	rout, wout, err := os.Pipe()
	if err != nil {
		return -1, errors.WithStack(err)
	}

	go io.Copy(os.Stdout, rout)

	cmd := exec.Command("/runner/bin/Runner.Worker", "spawnclient", "3", "4")
	cmd.Dir = "/runner/bin"
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{rin, wout}

	err = cmd.Start()
	if err != nil {
		return -1, errors.WithStack(err)
	}

	var msgType int32 = 1 // NewJobRequest
	err = binary.Write(win, binary.LittleEndian, msgType)
	if err != nil {
		return -1, errors.WithStack(err)
	}

	enc := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewEncoder()
	msg16, err := enc.Bytes(payload)

	var msgLen int32 = int32(len(msg16))
	err = binary.Write(win, binary.LittleEndian, msgLen)
	if err != nil {
		return -1, errors.WithStack(err)
	}

	_, err = win.Write(msg16)
	if err != nil {
		return -1, errors.WithStack(err)
	}

	err = cmd.Wait()
	if err == nil {
		return -1, errors.New("unexpected zero exit code (should be >=100)")
	}

	perr := err.(*exec.ExitError)
	code := perr.ExitCode()
	return code, nil
}

func decrypt(ciphertext string, identities []age.Identity) ([]byte, error) {
	encryptedPayload, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	r, err := age.Decrypt(bytes.NewReader(encryptedPayload), identities...)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	payloadbuf := &bytes.Buffer{}
	_, err = io.Copy(payloadbuf, r)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	payload := payloadbuf.Bytes()
	return payload, nil
}

func decryptorIdentities(ciphertext string, kapi *kms.KMS) ([]age.Identity, error) {
	encryptedKey, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	kresp, err := kapi.Decrypt(&kms.DecryptInput{CiphertextBlob: encryptedKey})
	if err != nil {
		return nil, errors.WithStack(err)
	}

	identities, err := age.ParseIdentities(bytes.NewReader(kresp.Plaintext))
	return identities, errors.WithStack(err)
}

func popEnv(name string) string {
	val := os.Getenv(name)
	_ = os.Unsetenv(name)
	return val
}
