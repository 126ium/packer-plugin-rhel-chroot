package chroot

import (
	"errors"
	"fmt"
	"log"
	"runtime"

	"github.com/hashicorp/packer/common"
	"github.com/hashicorp/packer/helper/config"
	"github.com/hashicorp/packer/helper/multistep"
	"github.com/hashicorp/packer/packer"
	"github.com/hashicorp/packer/template/interpolate"
)

// Config represents a configuration of builder.
type Config struct {
	common.PackerConfig `mapstructure:",squash"`

	OutputDir      string     `mapstructure:"output_directory"`
	WorkDir        string     `mapstructure:"tmp_directory"`
	ImageName      string     `mapstructure:"image_name"`
	MountPath      string     `mapstructure:"mount_path"`
	MountOptions   []string   `mapstructure:"mount_options"`
	BaseRPMS       []string   `mapstructure:"base_rpms"`
	ChrootMounts   [][]string `mapstructure:"chroot_mounts"`
	CopyFiles      []string   `mapstructure:"copy_files"`
	CommandWrapper string     `mapstructure:"command_wrapper"`
	InitChroot     bool       `mapstructure:"init_chroot"`


	ctx interpolate.Context
}

// Cleaner is an interface with a function for cleanup.
type Cleaner interface {
	CleanupFunc(multistep.StateBag) error
}

// Builder represents a builder plugin for Packer.
type Builder struct {
	config Config
	runner multistep.Runner
}

// NewBuilder returns a Builder.
func NewBuilder() *Builder {
	return new(Builder)
}

// Prepare validates given configuration.
func (b *Builder) Prepare(raws ...interface{}) ([]string, error) {
	err := config.Decode(&b.config, &config.DecodeOpts{
		Interpolate:        true,
		InterpolateContext: &b.config.ctx,
	}, raws...)
	if err != nil {
		return nil, err
	}

	if b.config.OutputDir == "" {
		b.config.OutputDir = fmt.Sprintf("output-%s", b.config.PackerBuildName)
	}

	if b.config.ImageName == "" {
		b.config.ImageName = fmt.Sprintf("packer-%s", b.config.PackerBuildName)
	}

	if b.config.MountPath == "" {
		b.config.MountPath = "/mnt/packer-builder-rhel-chroot/{{.ImageName}}"
	}

	if b.config.ChrootMounts == nil {
		b.config.ChrootMounts = make([][]string, 0)
	}

	if len(b.config.ChrootMounts) == 0 {
		b.config.ChrootMounts = [][]string{
			{"proc", "proc", "/proc"},
			{"sysfs", "sysfs", "/sys"},
			{"bind", "/dev", "/dev"},
			{"devpts", "devpts", "/dev/pts"},
			{"binfmt_misc", "binfmt_misc", "/proc/sys/fs/binfmt_misc"},
		}
	}

	if b.config.CopyFiles == nil {
		b.config.CopyFiles = []string{"/etc/resolv.conf"}
	}

	if b.config.CommandWrapper == "" {
		b.config.CommandWrapper = "{{.Command}}"
	}

	if b.config.MountPath == "" {
		b.config.MountPath = "/mnt/packer-builder-qemu-chroot/{{.ImageName}}"
	}

	// Accumulate any errors or warnings
	var errs *packer.MultiError
	var warns []string


	if errs != nil && len(errs.Errors) > 0 {
		return warns, errs
	}

	return warns, nil
}

// Run runs each step of the plugin in order.
func (b *Builder) Run(ui packer.Ui, hook packer.Hook, cache packer.Cache) (packer.Artifact, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("The rhel-chroot builder only works on Linux environments.")
	}

	state := new(multistep.BasicStateBag)
	state.Put("config", &b.config)
	state.Put("hook", hook)
	state.Put("ui", ui)
	state.Put("command_wrapper", NewCommandWrapper(b.config))

	steps := []multistep.Step{
		&StepPrepareOutputDir{},
		&StepPrepareImage{},
		&StepMountExtra{},
		&StepCopyFiles{},
		&StepChrootProvision{},
		&StepEarlyCleanup{},
		&StepCompressImage{},
	}

	b.runner = common.NewRunner(steps, b.config.PackerConfig, ui)
	b.runner.Run(state)

	if rawErr, ok := state.GetOk("error"); ok {
		return nil, rawErr.(error)
	}

	if _, ok := state.GetOk(multistep.StateCancelled); ok {
		return nil, errors.New("Build was cancelled.")
	}

	if _, ok := state.GetOk(multistep.StateHalted); ok {
		return nil, errors.New("Build was halted.")
	}

	artifact := &Artifact{
		dir: b.config.OutputDir,
		files: []string{
			state.Get("image_path").(string),
		},
	}

	return artifact, nil
}

// Cancel executes processing at cancel.
func (b *Builder) Cancel() {
	if b.runner != nil {
		log.Println("Cancelling the step runner...")
		b.runner.Cancel()
	}
}
