package runner

import (
	"context"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/khulnasoft-lab/YaraSec/constants"
	"github.com/khulnasoft-lab/YaraSec/pkg/output"
	"github.com/khulnasoft-lab/YaraSec/pkg/scan"
	"github.com/khulnasoft-lab/YaraSec/pkg/server"
	"github.com/khulnasoft-lab/YaraSec/pkg/yararules"
	"github.com/khulnasoft-lab/golang_sdk/utils/tasks"
	cfg "github.com/khulnasoft-lab/syncscan/pkg/config"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

type RunnerOptions struct {
	SocketPath                                                      string
	RulesPath                                                       string
	RulesListingURL                                                 string
	HostMountPath                                                   string
	FailOnCompileWarning                                            bool
	Local                                                           string
	ImageName                                                       string
	ContainerID                                                     string
	ConsoleURL                                                      string
	ConsolePort                                                     int
	KhulnasoftKey                                                   string
	OutFormat                                                       string
	FailOnHighCount, FailOnMediumCount, FailOnLowCount, FailOnCount int
	InactiveThreshold                                               int
}

func StartYaraSec[T any](ctx context.Context,
	opts RunnerOptions,
	config cfg.Config,
	constructServer func(srv *server.GRPCScannerServer) T,
	attachRegistrar func(s grpc.ServiceRegistrar, impl any)) {

	if opts.SocketPath == "" {
		runOnce(ctx, opts, config)
		return
	}

	base, err := server.NewGRPCScannerServer(
		opts.HostMountPath,
		opts.SocketPath,
		opts.RulesPath,
		opts.InactiveThreshold,
		opts.FailOnCompileWarning, config, constants.PluginName,
	)
	if err != nil {
		log.Fatalf("Cannot init grpc: %v", err)
	}
	go func() {

		svc := constructServer(base)

		if err := server.RunGrpcServer(ctx,
			opts.SocketPath,
			&svc,
			attachRegistrar,
		); err != nil {
			log.Panicf("main: failed to serve: %v", err)
		}
	}()

	<-ctx.Done()
}

func runOnce(ctx context.Context, opts RunnerOptions, extractorConfig cfg.Config) {
	var results IOCWriter

	yaraRules := yararules.New(opts.RulesPath)
	err := yaraRules.Compile(constants.Filescan, opts.FailOnCompileWarning)
	if err != nil {
		log.Errorf("error in runOnce compiling yara rules: %s", err)
		return
	}

	yaraScanner, err := yaraRules.NewScanner()
	if err != nil {
		log.Error("error in runOnce creating yara scanner:", err)
		return
	}

	scanner := scan.New(opts.HostMountPath, extractorConfig, yaraScanner, "")

	outputs := []output.IOCFound{}
	writeToArray := func(res output.IOCFound, scanID string) {
		outputs = append(outputs, res)
	}

	scanCtx := tasks.ScanContext{
		Res:     nil,
		IsAlive: atomic.Bool{},
		Context: ctx,
	}

	var st scan.ScanType
	nodeID := ""
	switch {
	case len(opts.Local) > 0:
		st = scan.DirScan
		nodeID = opts.Local
		log.Infof("scan for malwares in path %s", nodeID)
		err = scanner.Scan(&scanCtx, st, "", opts.Local, "", writeToArray)
		results = &output.JSONDirIOCOutput{DirName: nodeID, IOC: removeDuplicateIOCs(outputs)}
	case len(opts.ImageName) > 0:
		st = scan.ImageScan
		nodeID = opts.ImageName
		log.Infof("Scanning image %s for IOC...", nodeID)
		err = scanner.Scan(&scanCtx, st, "", opts.ImageName, "", writeToArray)
		results = &output.JSONImageIOCOutput{ImageID: nodeID, IOC: removeDuplicateIOCs(outputs)}
	case len(opts.ContainerID) > 0:
		st = scan.ContainerScan
		nodeID = opts.ContainerID
		log.Infof("scan for malwares in container %s", nodeID)
		err = scanner.Scan(&scanCtx, st, "", nodeID, "", writeToArray)
		results = &output.JSONImageIOCOutput{ContainerID: nodeID, IOC: removeDuplicateIOCs(outputs)}
	default:
		err = fmt.Errorf("invalid request")
	}

	results.SetTime()

	if err != nil {
		println(err.Error())
		return
	}

	if len(opts.ConsoleURL) != 0 && len(opts.KhulnasoftKey) != 0 {
		pub, err := output.NewPublisher(opts.ConsoleURL, strconv.Itoa(opts.ConsolePort), opts.KhulnasoftKey)
		if err != nil {
			log.Error(err.Error())
		}

		pub.SendReport(output.GetHostname(), opts.ImageName, opts.ContainerID, scan.ScanTypeString(st))
		scanID := pub.StartScan(nodeID, scan.ScanTypeString(st))
		if len(scanID) == 0 {
			scanID = fmt.Sprintf("%s-%d", nodeID, time.Now().UnixMilli())
		}
		if err := pub.IngestSecretScanResults(scanID, results.GetIOC()); err != nil {
			log.Errorf("IngestSecretScanResults: %v", err)
		}
		log.Infof("scan id %s", scanID)
	}

	counts := output.CountBySeverity(results.GetIOC())

	if opts.OutFormat == "json" {
		log.Infof("result severity counts: %+v", counts)
		err = results.WriteJSON()
		if err != nil {
			log.Errorf("error while writing IOC: %s", err)
			return
		}
	} else {
		fmt.Println("summary:")
		fmt.Printf("  total=%d high=%d medium=%d low=%d\n",
			counts.Total, counts.High, counts.Medium, counts.Low)
		err = results.WriteTable()
		if err != nil {
			log.Errorf("error while writing IOC: %s", err)
			return
		}
	}

	if results == nil {
		log.Error("set either -local or -image-name flag")
		return
	}

	output.FailOn(counts,
		opts.FailOnHighCount, opts.FailOnMediumCount, opts.FailOnLowCount, opts.FailOnCount)
}
