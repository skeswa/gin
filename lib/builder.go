package sparkplug

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

type Builder interface {
	Build() error
	Binary() string
	Errors() string
}

type builder struct {
	dir      string
	binary   string
	errors   string
	useGodep bool
}

func NewBuilder(dir string, bin string, useGodep bool) Builder {
	if len(bin) == 0 {
		bin = "bin"
	}

	// does not work on Windows without the ".exe" extension
	if runtime.GOOS == "windows" {
		if !strings.HasSuffix(bin, ".exe") { // check if it already has the .exe extension
			bin += ".exe"
		}
	}

	return &builder{dir: dir, binary: bin, useGodep: useGodep}
}

func (b *builder) Binary() string {
	return b.binary
}

func (b *builder) Errors() string {
	return b.errors
}

func (b *builder) Build() error {
	var command *exec.Cmd
	if b.useGodep {
		command = exec.Command("godep", "go", "build", "-o", b.binary)
	} else {
		command = exec.Command("go", "build", "-o", b.binary)
	}
	command.Dir = b.dir

	output, err := command.CombinedOutput()
	if err != nil || !command.ProcessState.Success() {
		b.errors = string(output)
	} else {
		b.errors = ""
	}

	if len(b.errors) > 0 {
		return fmt.Errorf(b.errors)
	}

	return err
}
