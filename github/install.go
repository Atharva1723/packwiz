package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/packwiz/packwiz/core"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var GithubRegex = regexp.MustCompile(`^https?://(?:www\.)?github\.com/([^/]+/[^/]+)`)

// installCmd represents the install command
var installCmd = &cobra.Command{
	Use:     "add [URL]",
	Short:   "Add a project from a GitHub repository URL",
	Aliases: []string{"install", "get"},
	Args:    cobra.ArbitraryArgs,
	Run: func(cmd *cobra.Command, args []string) {
		pack, err := core.LoadPack()
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		if len(args) == 0 || len(args[0]) == 0 {
			fmt.Println("You must specify a GitHub repository URL.")
			os.Exit(1)
		}

		// Try interpreting the argument as a slug, or GitHub repository URL.
		var slug string

		// Check if the argument is a valid GitHub repository URL; if so, extract the slug from the URL.
		// Otherwise, interpret the argument as a slug directly.
		matches := GithubRegex.FindStringSubmatch(args[0])
		if len(matches) == 2 {
			slug = matches[1]
		} else {
			slug = args[0]
		}

		repo, err := fetchRepo(slug)

		if err != nil {
			fmt.Println("Failed to get the mod ", err)
			os.Exit(1)
		}

		installMod(repo, pack)
	},
}

func init() {
	githubCmd.AddCommand(installCmd)
}

const githubApiUrl = "https://api.github.com/"

func installMod(repo Repo, pack core.Pack) error {
	latestVersion, err := getLatestVersion(repo.FullName, "")
	if err != nil {
		return fmt.Errorf("failed to get latest version: %v", err)
	}
	if latestVersion.URL == "" {
		return errors.New("mod is not available for this Minecraft version (use the acceptable-game-versions option to accept more) or mod loader")
	}

	return installVersion(repo, latestVersion, pack)
}

func getLatestVersion(slug string, branch string) (Release, error) {
	var modReleases []Release
	var release Release

	resp, err := ghDefaultClient.makeGet(slug)
	if err != nil {
		return release, err
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return release, err
	}
	err = json.Unmarshal(body, &modReleases)
	if err != nil {
		return release, err
	}
	for _, r := range modReleases {
		if r.TargetCommitish == branch {
			return r, nil
		}
	}

	return modReleases[0], nil
}

func installVersion(repo Repo, release Release, pack core.Pack) error {
	var files = release.Assets

	if len(files) == 0 {
		return errors.New("release doesn't have any files attached")
	}

	// TODO: add some way to allow users to pick which file to install?
	var file = files[0]
	for _, v := range release.Assets {
		if strings.HasSuffix(v.Name, ".jar") {
			file = v
		}
	}

	//Install the file
	fmt.Printf("Installing %s from release %s\n", file.Name, release.TagName)
	index, err := pack.LoadIndex()
	if err != nil {
		return err
	}

	updateMap := make(map[string]map[string]interface{})

	updateMap["github"], err = ghUpdateData{
		Slug:   repo.FullName,
		Tag:    release.TagName,
		Branch: release.TargetCommitish,
	}.ToMap()
	if err != nil {
		return err
	}

	hash, err := file.getSha256()
	if err != nil {
		return err
	}

	modMeta := core.Mod{
		Name:     repo.Name,
		FileName: file.Name,
		Side:     core.UniversalSide,
		Download: core.ModDownload{
			URL:        file.BrowserDownloadURL,
			HashFormat: "sha256",
			Hash:       hash,
		},
		Update: updateMap,
	}
	var path string
	folder := viper.GetString("meta-folder")
	if folder == "" {
		folder = "mods"
	}
	path = modMeta.SetMetaPath(filepath.Join(viper.GetString("meta-folder-base"), folder, repo.Name+core.MetaExtension))

	// If the file already exists, this will overwrite it!!!
	// TODO: Should this be improved?
	// Current strategy is to go ahead and do stuff without asking, with the assumption that you are using
	// VCS anyway.

	format, hash, err := modMeta.Write()
	if err != nil {
		return err
	}
	err = index.RefreshFileWithHash(path, format, hash, true)
	if err != nil {
		return err
	}
	err = index.Write()
	if err != nil {
		return err
	}
	err = pack.UpdateIndexHash()
	if err != nil {
		return err
	}
	err = pack.Write()
	if err != nil {
		return err
	}
	return nil
}
