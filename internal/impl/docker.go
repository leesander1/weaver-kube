// Copyright 2023 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package impl

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
	"time"

	"github.com/ServiceWeaver/weaver/runtime/protos"
	"github.com/google/uuid"
)

// dockerfileTmpl contains the templatized content of the Dockerfile.
var dockerfileTmpl = template.Must(template.New("Dockerfile").Parse(`
{{if . }}
FROM golang:1.20-bullseye as builder
RUN echo ""{{range .}} && go install {{.}}{{end}}
{{end}}
FROM gcr.io/distroless/base-debian11
WORKDIR /weaver/
COPY . .
{{if . }}
COPY --from=builder /go/bin/ /weaver/
{{end}}
ENTRYPOINT ["/weaver/weaver-kube"]
`))

// imageSpecs holds information about a container image build.
type imageSpecs struct {
	name      string   // name is the name of the image to build
	files     []string // files that should be copied to the container
	goInstall []string // binary targets that should be 'go install'-ed
}

// BuildAndUploadDockerImage builds a docker image and upload it to docker hub.
func BuildAndUploadDockerImage(ctx context.Context, dep *protos.Deployment, image string) (string, error) {
	// Create the docker image specifications.
	specs, err := buildImageSpecs(dep, image)
	if err != nil {
		return "", fmt.Errorf("unable to build image specs: %w", err)
	}

	// Build the docker image.
	if err := buildImage(ctx, specs); err != nil {
		return "", fmt.Errorf("unable to create image: %w", err)
	}

	// Upload the docker image to docker hub.
	if err := uploadImage(ctx, specs.name); err != nil {
		return "", fmt.Errorf("unable to upload image: %w", err)
	}
	return specs.name, nil
}

// buildImage creates a docker image with specs.
func buildImage(ctx context.Context, specs *imageSpecs) error {
	fmt.Fprintf(os.Stderr, greenText(), fmt.Sprintf("Building image %s...", specs.name))
	// Create:
	//  workDir/
	//    file1
	//    file2
	//    ...
	//    fileN
	//    Dockerfile   - docker build instructions
	//    tool binary
	ctx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	// Create workDir/.
	workDir := filepath.Join(os.TempDir(), fmt.Sprintf("weaver%s", uuid.New().String()))
	if err := os.Mkdir(workDir, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(workDir)

	// Copy the files from specs to workDir/.
	for _, file := range specs.files {
		workDirFile := filepath.Join(workDir, filepath.Base(filepath.Clean(file)))
		if err := cp(file, workDirFile); err != nil {
			return err
		}
	}

	// Create a Dockerfile in workDir/.
	dockerFile, err := os.Create(filepath.Join(workDir, dockerfileTmpl.Name()))
	if err != nil {
		return err
	}
	if err := dockerfileTmpl.Execute(dockerFile, specs.goInstall); err != nil {
		dockerFile.Close()
		return err
	}
	if err := dockerFile.Close(); err != nil {
		return err
	}
	return dockerBuild(ctx, workDir, specs.name)
}

// Use docker-cli to build the docker image.
func dockerBuild(ctx context.Context, buildContext, tag string) error {
	c := exec.CommandContext(ctx, "docker", "build", buildContext, "-t", tag)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// uploadImage upload image appImage to docker hub.
func uploadImage(ctx context.Context, appImage string) error {
	fmt.Fprintf(os.Stderr, greenText(), fmt.Sprintf("\nUploading image %s...", appImage))

	c := exec.CommandContext(ctx, "docker", "push", appImage)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// buildImageSpecs build the docker image specs for an app deployment.
func buildImageSpecs(dep *protos.Deployment, image string) (*imageSpecs, error) {
	return &imageSpecs{
		name:      fmt.Sprintf("%s:%s", image, dep.Id[:8]),
		files:     []string{dep.App.Binary},
		goInstall: []string{"github.com/ServiceWeaver/weaver-kube/cmd/weaver-kube@latest"},
	}, nil
}
