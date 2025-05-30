package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"

	utils "github.com/khulnasoft-lab/YaraSec/utils"
	log "github.com/sirupsen/logrus"
)

func ScheduleYaraSecUpdater(ctx context.Context, opts RunnerOptions) {
	if opts.SocketPath != "" {
		ticker := time.NewTicker(10 * time.Hour)
		for {
			fmt.Println("Updater invoked")
			err := StartYaraSecUpdater(opts.RulesPath, filepath.Dir(opts.RulesPath), opts.RulesListingURL)
			if err != nil {
				log.Panicf("main: failed to serve: %v", err)
			}

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}
}

func StartYaraSecUpdater(rulesPath, configPath, rulesListingURL string) error {
	yaraRuleUpdater, err := NewYaraRuleUpdater(rulesPath)
	if err != nil {
		log.Errorf("main: failed to serve: %v", err)
		return err
	}
	_, err = utils.DownloadFile(rulesListingURL, configPath)
	if err != nil {
		log.Errorf("main: failed to serve: %v", err)
		return err
	}
	content, err := os.ReadFile(filepath.Join(configPath, "/listing.json"))
	if err != nil {
		log.Errorf("main: failed to serve: %v", err)
		return err
	}
	var yaraRuleListingJSON YaraRuleListing
	err = json.Unmarshal(content, &yaraRuleListingJSON)
	if err != nil {
		log.Errorf("main: failed to serve: %v", err)
		return err
	}
	if len(yaraRuleListingJSON.Available.V3) > 0 {
		if yaraRuleListingJSON.Available.V3[0].Checksum != yaraRuleUpdater.currentFileChecksum {
			yaraRuleUpdater.currentFileChecksum = yaraRuleListingJSON.Available.V3[0].Checksum
			file, err := json.MarshalIndent(yaraRuleUpdater, "", " ")
			if err != nil {
				log.Errorf("main: failed to marshal: %v", err)
				return err
			}
			err = os.WriteFile(path.Join(rulesPath, "metaListingData.json"), file, 0644)
			if err != nil {
				log.Errorf("main: failed to write to metaListingData.json: %v", err)
				return err
			}
			fileName, err := utils.DownloadFile(yaraRuleListingJSON.Available.V3[0].URL, configPath)
			if err != nil {
				log.Errorf("main: failed to download file: %v", err)
				return err
			}

			if utils.PathExists(filepath.Join(configPath, fileName)) {
				log.Infof("rule file exists: %s", filepath.Join(configPath, fileName))

				readFile, readErr := os.OpenFile(filepath.Join(configPath, fileName), os.O_CREATE|os.O_RDWR, 0755)
				if readErr != nil {
					log.Errorf("main: failed to open rules tar file : %v", readErr)
					return readErr
				}

				defer readFile.Close()

				newFile, err := utils.CreateFile(configPath, "malware.yar")
				if err != nil {
					log.Errorf("main: failed to create malware.yar: %v", err)
					return err
				}

				defer newFile.Close()

				err = utils.Untar(newFile, readFile)
				if err != nil {
					log.Errorf("main: failed to untar: %v", err)
					return err
				}
			}
		}
	}
	return nil
}

func NewYaraRuleUpdater(rulesPath string) (*YaraRuleUpdater, error) {
	updater := &YaraRuleUpdater{
		yaraRuleListingJSON:  YaraRuleListing{},
		yaraRulePath:         path.Join(rulesPath, "metaListingData.json"),
		downloadYaraRulePath: "",
	}
	if utils.PathExists(updater.yaraRulePath) {
		content, err := os.ReadFile(updater.yaraRulePath)
		if err != nil {
			return nil, err
		}
		err = json.Unmarshal(content, &updater)
		if err != nil {
			return nil, err
		}
	}
	return updater, nil
}
