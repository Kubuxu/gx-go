package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	cli "github.com/codegangsta/cli"
	gx "github.com/whyrusleeping/gx/gxutil"
	. "github.com/whyrusleeping/stump"
)

// for go packages, extra info
type GoInfo struct {
	DvcsImport string `json:"dvcsimport,omitempty"`

	// GoVersion sets a compiler version requirement, users will be warned if installing
	// a package using an unsupported compiler
	GoVersion string `json:"goversion,omitempty"`
}

type Package struct {
	gx.PackageBase

	Gx GoInfo `json:"gx,omitempty"`
}

func LoadPackageFile(name string) (*Package, error) {
	fi, err := os.Open(name)
	if err != nil {
		return nil, err
	}

	var pkg Package
	err = json.NewDecoder(fi).Decode(&pkg)
	if err != nil {
		return nil, err
	}

	return &pkg, nil
}

func main() {
	app := cli.NewApp()
	app.Name = "gx-go"
	app.Author = "whyrusleeping"
	app.Usage = "gx extensions for golang"
	app.Version = "0.2.0"

	var UpdateCommand = cli.Command{
		Name:      "update",
		Usage:     "update a packages imports to a new path",
		ArgsUsage: "[old import] [new import]",
		Action: func(c *cli.Context) {
			if len(c.Args()) < 2 {
				fmt.Println("must specify current and new import names")
				return
			}

			oldimp := c.Args()[0]
			newimp := c.Args()[1]

			err := doUpdate(oldimp, newimp)
			if err != nil {
				Fatal(err)
			}
		},
	}

	var ImportCommand = cli.Command{
		Name:  "import",
		Usage: "import a go package and all its depencies into gx",
		Flags: []cli.Flag{
			cli.BoolFlag{
				Name:  "rewrite",
				Usage: "rewrite import paths to use vendored packages",
			},
			cli.BoolFlag{
				Name:  "yesall",
				Usage: "assume defaults for all options",
			},
		},
		Action: func(c *cli.Context) {
			importer, err := NewImporter(c.Bool("rewrite"))
			if err != nil {
				Fatal(err)
			}

			importer.yesall = c.Bool("yesall")

			if !c.Args().Present() {
				Fatal("must specify a package name")
			}

			pkg := c.Args().First()
			Log("vendoring package %s", pkg)

			_, err = importer.GxPublishGoPackage(pkg)
			if err != nil {
				Fatal(err)
			}
		},
	}

	var PathCommand = cli.Command{
		Name:  "path",
		Usage: "prints the import path of the current package within GOPATH",
		Action: func(c *cli.Context) {
			gopath := os.Getenv("GOPATH")
			if gopath == "" {
				Fatal("GOPATH not set, cannot derive import path")
			}

			cwd, err := os.Getwd()
			if err != nil {
				Fatal(err)
			}

			srcdir := path.Join(gopath, "src")
			srcdir += "/"

			if !strings.HasPrefix(cwd, srcdir) {
				Fatal("package not within GOPATH/src")
			}

			rel := cwd[len(srcdir):]
			fmt.Println(rel)
		},
	}

	var HookCommand = cli.Command{
		Name:  "hook",
		Usage: "go specific hooks to be called by the gx tool",
		Subcommands: []cli.Command{
			postImportCommand,
			reqCheckCommand,
			postInitHookCommand,
		},
		Action: func(c *cli.Context) {},
	}

	app.Commands = []cli.Command{
		UpdateCommand,
		ImportCommand,
		PathCommand,
		HookCommand,
	}

	app.Run(os.Args)
}

func prompt(text, def string) (string, error) {
	scan := bufio.NewScanner(os.Stdin)
	fmt.Printf("%s (default: '%s') ", text, def)
	for scan.Scan() {
		if scan.Text() != "" {
			return scan.Text(), nil
		}
		return def, nil
	}

	return "", scan.Err()
}

func yesNoPrompt(prompt string, def bool) bool {
	opts := "[y/N]"
	if def {
		opts = "[Y/n]"
	}

	fmt.Printf("%s %s ", prompt, opts)
	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		val := strings.ToLower(scan.Text())
		switch val {
		case "":
			return def
		case "y":
			return true
		case "n":
			return false
		default:
			fmt.Println("please type 'y' or 'n'")
		}
	}

	panic("unexpected termination of stdin")
}

var postImportCommand = cli.Command{
	Name:  "post-import",
	Usage: "hook called after importing a new go package",
	Action: func(c *cli.Context) {
		if !c.Args().Present() {
			Fatal("no package specified")
		}
		dephash := c.Args().First()

		pkg, err := LoadPackageFile(gx.PkgFileName)
		if err != nil {
			Fatal(err)
		}

		err = postImportHook(pkg, dephash)
		if err != nil {
			Fatal(err)
		}
	},
}

var reqCheckCommand = cli.Command{
	Name:  "req-check",
	Usage: "hook called to check if requirements of a package are met",
	Action: func(c *cli.Context) {
		if !c.Args().Present() {
			Fatal("no package specified")
		}
		dephash := c.Args().First()

		err := reqCheckHook(dephash)
		if err != nil {
			Fatal(err)
		}
	},
}

var postInitHookCommand = cli.Command{
	Name:  "post-init",
	Usage: "hook called to perform go specific package initialization",
	Action: func(c *cli.Context) {
		pkg, err := LoadPackageFile(gx.PkgFileName)
		if err != nil {
			Fatal(err)
		}
		cwd, err := os.Getwd()
		if err != nil {
			Fatal(err)
		}

		imp, _ := packagesGoImport(cwd)

		if imp != "" {
			pkg.Gx.DvcsImport = imp
		}

		err = gx.SavePackageFile(pkg, gx.PkgFileName)
		if err != nil {
			Fatal(err)
		}
	},
}

func packagesGoImport(p string) (string, error) {
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		return "", fmt.Errorf("GOPATH not set, cannot derive import path")
	}

	srcdir := path.Join(gopath, "src")
	srcdir += "/"

	if !strings.HasPrefix(p, srcdir) {
		return "", fmt.Errorf("package not within GOPATH/src")
	}

	return p[len(srcdir):], nil
}

func postImportHook(pkg *Package, npkgHash string) error {
	npkgPath := filepath.Join("vendor", npkgHash)

	var npkg Package
	err := gx.FindPackageInDir(&npkg, npkgPath)
	if err != nil {
		return err
	}

	if npkg.Gx.DvcsImport != "" {
		q := fmt.Sprintf("update imports of %s to the newly imported package?", npkg.Gx.DvcsImport)
		if yesNoPrompt(q, false) {
			nimp := fmt.Sprintf("%s/%s", npkgHash, npkg.Name)
			err := doUpdate(npkg.Gx.DvcsImport, nimp)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func reqCheckHook(pkghash string) error {
	p := filepath.Join("vendor", pkghash)

	var npkg Package
	err := gx.FindPackageInDir(&npkg, p)
	if err != nil {
		return err
	}

	if npkg.Gx.GoVersion != "" {
		out, err := exec.Command("go", "version").CombinedOutput()
		if err != nil {
			return fmt.Errorf("no go compiler installed")
		}

		parts := strings.Split(string(out), " ")
		if len(parts) < 4 {
			return fmt.Errorf("unrecognized output from go compiler")
		}

		havevers := parts[2][2:]

		reqvers := npkg.Gx.GoVersion

		badreq, err := versionComp(havevers, reqvers)
		if err != nil {
			return err
		}
		if badreq {
			return fmt.Errorf("package '%s' requires go version %s, you have %s installed.", npkg.Name, reqvers, havevers)
		}

	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func versionComp(have, req string) (bool, error) {
	hp := strings.Split(have, ".")
	rp := strings.Split(req, ".")

	l := min(len(hp), len(rp))
	hp = hp[:l]
	rp = rp[:l]
	for i, v := range hp {
		hv, err := strconv.Atoi(v)
		if err != nil {
			return false, err
		}

		rv, err := strconv.Atoi(rp[i])
		if err != nil {
			return false, err
		}

		if hv < rv {
			return true, nil
		}
	}
	return false, nil
}
