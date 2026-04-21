package install

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/c4milo/unpackit"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
)

var BuildTarget string

// SysvScript Keep it in sync with the template from service_sysv_linux.go file
// Use "ps | grep -v grep | grep $(get_pid)" because "ps PID" may not work on OpenWrt
const SysvScript = `#!/bin/sh
# For RedHat and cousins:
# chkconfig: - 99 01
# description: {{.Description}}
# processname: {{.Path}}
### BEGIN INIT INFO
# Provides:          {{.Path}}
# Required-Start:
# Required-Stop:
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: {{.DisplayName}}
# Description:       {{.Description}}
### END INIT INFO
cmd="{{.Path}}{{range .Arguments}} {{.|cmd}}{{end}}"
name=$(basename $(readlink -f $0))
pid_file="/var/run/$name.pid"
stdout_log="/var/log/$name.log"
stderr_log="/var/log/$name.err"
[ -e /etc/sysconfig/$name ] && . /etc/sysconfig/$name
get_pid() {
    cat "$pid_file"
}
is_running() {
    [ -f "$pid_file" ] && ps | grep -v grep | grep $(get_pid) > /dev/null 2>&1
}
case "$1" in
    start)
        if is_running; then
            echo "Already started"
        else
            echo "Starting $name"
            {{if .WorkingDirectory}}cd '{{.WorkingDirectory}}'{{end}}
            $cmd >> "$stdout_log" 2>> "$stderr_log" &
            echo $! > "$pid_file"
            if ! is_running; then
                echo "Unable to start, see $stdout_log and $stderr_log"
                exit 1
            fi
        fi
    ;;
    stop)
        if is_running; then
            echo -n "Stopping $name.."
            kill $(get_pid)
            for i in $(seq 1 10)
            do
                if ! is_running; then
                    break
                fi
                echo -n "."
                sleep 1
            done
            echo
            if is_running; then
                echo "Not stopped; may still be shutting down or shutdown may have failed"
                exit 1
            else
                echo "Stopped"
                if [ -f "$pid_file" ]; then
                    rm "$pid_file"
                fi
            fi
        else
            echo "Not running"
        fi
    ;;
    restart)
        $0 stop
        if is_running; then
            echo "Unable to stop, will not attempt to start"
            exit 1
        fi
        $0 start
    ;;
    status)
        if is_running; then
            echo "Running"
        else
            echo "Stopped"
            exit 1
        fi
    ;;
    *)
    echo "Usage: $0 {start|stop|restart|status}"
    exit 1
    ;;
esac
exit 0
`

const SystemdScript = `[Unit]
Description={{.Description}}
ConditionFileIsExecutable={{.Path|cmdEscape}}
{{range $i, $dep := .Dependencies}}
{{$dep}} {{end}}
[Service]
LimitNOFILE=65536
StartLimitInterval=5
StartLimitBurst=10
ExecStart={{.Path|cmdEscape}}{{range .Arguments}} {{.|cmd}}{{end}}
{{if .ChRoot}}RootDirectory={{.ChRoot|cmd}}{{end}}
{{if .WorkingDirectory}}WorkingDirectory={{.WorkingDirectory|cmdEscape}}{{end}}
{{if .UserName}}User={{.UserName}}{{end}}
{{if .ReloadSignal}}ExecReload=/bin/kill -{{.ReloadSignal}} "$MAINPID"{{end}}
{{if .PIDFile}}PIDFile={{.PIDFile|cmd}}{{end}}
{{if and .LogOutput .HasOutputFileSupport -}}
StandardOutput=file:/var/log/{{.Name}}.out
StandardError=file:/var/log/{{.Name}}.err
{{- end}}
Restart=always
RestartSec=120
[Install]
WantedBy=multi-user.target
`

func UpdateNps() error {
	destPath, err := downloadLatest("server")
	if err != nil {
		return err
	}
	// Copy the downloaded payload into the install location.
	if _, err := copyStaticFile(destPath, "nps"); err != nil {
		return err
	}
	fmt.Println("Update completed, please restart")
	return nil
}

func UpdateNpc() error {
	destPath, err := downloadLatest("client")
	if err != nil {
		return err
	}
	// Copy the downloaded payload into the install location.
	if _, err := copyStaticFile(destPath, "npc"); err != nil {
		return err
	}
	fmt.Println("Update completed, please restart")
	return nil
}

type release struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Digest             string `json:"digest"`
	} `json:"assets"`
}

func buildDownloadTLSConfig(addr string) *tls.Config {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	cfg := &tls.Config{}
	if net.ParseIP(host) == nil {
		cfg.ServerName = host
	}
	return cfg
}

func downloadLatest(bin string) (string, error) {
	const timeout = 5 * time.Second
	const idleTimeout = 10 * time.Second
	const keepAliveTime = 30 * time.Second

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := &net.Dialer{
				Timeout:   timeout,
				KeepAlive: keepAliveTime,
			}
			raw, err := d.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return conn.NewTimeoutConn(raw, idleTimeout), nil
		},
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := &net.Dialer{
				Timeout:   timeout,
				KeepAlive: keepAliveTime,
			}
			raw, err := d.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return conn.NewTimeoutTLSConn(raw, buildDownloadTLSConfig(addr), idleTimeout, timeout)
		},
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
	}
	httpClient := &http.Client{
		Transport: transport,
	}

	useCDNLatest := true
	rl := new(release)
	var version string
	// get version
	data, err := httpClient.Get("https://api.github.com/repos/djylb/nps/releases/latest")
	if err == nil {
		defer func() { _ = data.Body.Close() }()
		b, err := io.ReadAll(data.Body)
		if err == nil {
			if err := json.Unmarshal(b, &rl); err == nil {
				version = rl.TagName
				if version != "" {
					useCDNLatest = false
				}
				fmt.Println("The latest version is", version)
			}
		}
	}
	if useCDNLatest {
		version = "latest"
		fmt.Println("GitHub API failed; use CDN @latest (skip hash).")
	}

	osName := runtime.GOOS
	archName := runtime.GOARCH

	var filename string
	switch {
	case BuildTarget == "win7":
		filename = fmt.Sprintf("%s_%s_%s_old.tar.gz", osName, archName, bin)
	case BuildTarget != "":
		filename = fmt.Sprintf("%s_%s_%s_%s.tar.gz", osName, archName, BuildTarget, bin)
	default:
		filename = fmt.Sprintf("%s_%s_%s.tar.gz", osName, archName, bin)
	}

	var expectedHash string
	if !useCDNLatest {
		for _, a := range rl.Assets {
			if a.Name != filename {
				continue
			}
			//fmt.Println("Expected Hash:", a.Digest)
			if strings.HasPrefix(a.Digest, "sha256:") {
				expectedHash = strings.TrimPrefix(a.Digest, "sha256:")
			}
			break
		}
		//fmt.Println("Expected SHA256:", expectedHash)
		if expectedHash == "" {
			fmt.Println("No SHA256 digest found for", filename, "; skipping hash check")
		}
	} else {
		expectedHash = ""
	}

	// download latest package
	var urls []string
	if useCDNLatest {
		urls = []string{
			fmt.Sprintf("https://cdn.jsdelivr.net/gh/djylb/nps-mirror@latest/%s", filename),
			fmt.Sprintf("https://fastly.jsdelivr.net/gh/djylb/nps-mirror@latest/%s", filename),
			fmt.Sprintf("https://github.com/djylb/nps/releases/latest/download/%s", filename),
			fmt.Sprintf("https://gcore.jsdelivr.net/gh/djylb/nps-mirror@latest/%s", filename),
			fmt.Sprintf("https://testingcf.jsdelivr.net/gh/djylb/nps-mirror@latest/%s", filename),
		}
	} else {
		urls = []string{
			fmt.Sprintf("https://github.com/djylb/nps/releases/download/%s/%s", version, filename),
			fmt.Sprintf("https://cdn.jsdelivr.net/gh/djylb/nps-mirror@%s/%s", version, filename),
			fmt.Sprintf("https://fastly.jsdelivr.net/gh/djylb/nps-mirror@%s/%s", version, filename),
			fmt.Sprintf("https://gcore.jsdelivr.net/gh/djylb/nps-mirror@%s/%s", version, filename),
			fmt.Sprintf("https://testingcf.jsdelivr.net/gh/djylb/nps-mirror@%s/%s", version, filename),
		}
	}

	var lastErr error
	for _, url := range urls {
		fmt.Println("Trying:", url)
		resp, err := httpClient.Get(url)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
			_ = resp.Body.Close()
			continue
		}

		var reader io.Reader = resp.Body
		var hasher hash.Hash
		if expectedHash != "" {
			hasher = sha256.New()
			reader = io.TeeReader(resp.Body, hasher)
		}

		destPath, err := os.MkdirTemp(os.TempDir(), "nps-")
		if err != nil {
			_ = resp.Body.Close()
			return "", fmt.Errorf("create temp directory: %w", err)
		}

		if err := unpackit.Unpack(reader, destPath); err != nil {
			_ = resp.Body.Close()
			_ = os.RemoveAll(destPath)
			fmt.Println("  → failed:", err)
			lastErr = err
			continue
		}
		_ = resp.Body.Close()

		if expectedHash != "" {
			sum := hex.EncodeToString(hasher.Sum(nil))
			if sum != expectedHash {
				fmt.Printf("  → checksum mismatch: got %s vs %s\n", sum, expectedHash)
				_ = os.RemoveAll(destPath)
				lastErr = fmt.Errorf("checksum mismatch")
				continue
			}
			//fmt.Printf("  → checksum verified: %s\n", sum)
		}

		if bin == "server" {
			destPath = strings.ReplaceAll(destPath, "/web", "")
			destPath = strings.ReplaceAll(destPath, `\web`, "")
			destPath = strings.ReplaceAll(destPath, "/views", "")
			destPath = strings.ReplaceAll(destPath, `\views`, "")
		} else {
			destPath = strings.ReplaceAll(destPath, `\conf`, "")
			destPath = strings.ReplaceAll(destPath, "/conf", "")
		}
		return destPath, nil
	}
	if lastErr == nil {
		lastErr = errors.New("download failed")
	}
	return "", fmt.Errorf("all mirrors failed: %w", lastErr)
}

func copyStaticFile(srcPath, bin string) (string, error) {
	path := common.GetInstallPath()
	if bin == "nps" {
		if err := CopyDir(filepath.Join(srcPath, "web", "views"), filepath.Join(path, "web", "views")); err != nil {
			if exists, _ := pathExists(filepath.Join(path, "web", "views")); exists {
				goto ExecPath
			}
			return "", err
		}
		chMod(filepath.Join(path, "web", "views"), 0766)
		if err := CopyDir(filepath.Join(srcPath, "web", "static"), filepath.Join(path, "web", "static")); err != nil {
			if exists, _ := pathExists(filepath.Join(path, "web", "static")); exists {
				goto ExecPath
			}
			return "", err
		}
		chMod(filepath.Join(path, "web", "static"), 0766)
		if _, err := copyFile(filepath.Join(srcPath, "conf", "nps.conf"), filepath.Join(path, "conf", "nps.conf.default")); err != nil {
			if exists, _ := pathExists(filepath.Join(path, "conf", "nps.conf")); exists {
				goto ExecPath
			}
			return "", err
		}
		chMod(filepath.Join(path, "conf", "nps.conf.default"), 0766)
	}
ExecPath:
	binPath, err := os.Executable()
	if err != nil {
		binPath, _ = filepath.Abs(os.Args[0])
	}

	if !common.IsWindows() {
		_, _ = copyFile(filepath.Join(srcPath, bin), binPath)
		chMod(binPath, 0755)
		if _, err := copyFile(filepath.Join(srcPath, bin), "/usr/bin/"+bin); err != nil {
			if _, err := copyFile(filepath.Join(srcPath, bin), "/usr/local/bin/"+bin); err != nil {
				return "", err
			}
			_, _ = copyFile(filepath.Join(srcPath, bin), "/usr/local/bin/"+bin+"-update")
			chMod("/usr/local/bin/"+bin+"-update", 0755)
			binPath = "/usr/local/bin/" + bin
		} else {
			_, _ = copyFile(filepath.Join(srcPath, bin), "/usr/bin/"+bin+"-update")
			chMod("/usr/bin/"+bin+"-update", 0755)
			binPath = "/usr/bin/" + bin
		}
	} else {
		_, _ = copyFile(filepath.Join(srcPath, bin+".exe"), filepath.Join(common.GetAppPath(), bin+"-update.exe"))
		_, _ = copyFile(filepath.Join(srcPath, bin+".exe"), filepath.Join(common.GetAppPath(), bin+".exe"))
	}
	chMod(binPath, 0755)
	return binPath, nil
}

func NPC() error {
	path := common.GetInstallPath()
	if !common.FileExists(path) {
		err := os.MkdirAll(path, 0755)
		if err != nil {
			return err
		}
	}
	_, err := copyStaticFile(common.GetAppPath(), "npc")
	return err
}

func NPS() (string, error) {
	path := common.GetInstallPath()
	log.Println("install path:" + path)
	if common.FileExists(path) {
		if err := MkidrDirAll(path, "web/static", "web/views"); err != nil {
			return "", err
		}
	} else {
		if err := MkidrDirAll(path, "conf", "web/static", "web/views"); err != nil {
			return "", err
		}
		// not copy config if the config file is exist
		if err := CopyDir(filepath.Join(common.GetAppPath(), "conf"), filepath.Join(path, "conf")); err != nil {
			return "", err
		}
		chMod(filepath.Join(path, "conf"), 0766)
	}
	binPath, err := copyStaticFile(common.GetAppPath(), "nps")
	if err != nil {
		return "", err
	}
	log.Println("install ok!")
	log.Println("Static files and configuration files in the current directory will be useless")
	log.Println("The new configuration file is located in", path, "you can edit them")
	if !common.IsWindows() {
		log.Println(`You can start with:
nps start|stop|restart|uninstall|update or nps-update update
anywhere!`)
	} else {
		log.Println(`You can copy executable files to any directory and start working with:
nps.exe start|stop|restart|uninstall|update or nps-update.exe update
now!`)
	}
	chMod(common.GetLogPath(), 0777)
	return binPath, nil
}

func MkidrDirAll(path string, v ...string) error {
	for _, item := range v {
		if err := os.MkdirAll(filepath.Join(path, item), 0755); err != nil {
			return fmt.Errorf("create directory %s: %w", filepath.Join(path, item), err)
		}
	}
	return nil
}

func CopyDir(srcPath string, destPath string) error {
	// Validate source and destination before copying any content.
	if srcInfo, err := os.Stat(srcPath); err != nil {
		//fmt.Println(err.Error())
		log.Println("Failed to copy source directory.")
		return err
	} else {
		if !srcInfo.IsDir() {
			return errors.New("srcPath is not a directory")
		}
	}
	if destInfo, err := os.Stat(destPath); err != nil {
		if os.IsNotExist(err) {
			if mkErr := os.MkdirAll(destPath, os.ModePerm); mkErr != nil {
				return mkErr
			}
		} else {
			return err
		}
	} else {
		if !destInfo.IsDir() {
			return errors.New("destInfo is not the right directory")
		}
	}
	return filepath.Walk(srcPath, func(path string, f os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if f == nil {
			return nil
		}
		if !f.IsDir() {
			relPath, relErr := filepath.Rel(srcPath, path)
			if relErr != nil {
				return relErr
			}
			destNewPath := filepath.Join(destPath, relPath)
			log.Println("copy file: " + path + " -> " + destNewPath)
			if _, copyErr := copyFile(path, destNewPath); copyErr != nil {
				return copyErr
			}
			if !common.IsWindows() {
				chMod(destNewPath, 0766)
			}
		}
		return nil
	})
}

func copyFile(src, dest string) (w int64, err error) {
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return 0, err
	}
	destAbs, err := filepath.Abs(dest)
	if err != nil {
		return 0, err
	}
	if srcAbs == destAbs {
		return 0, nil
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return
	}
	defer func() { _ = srcFile.Close() }()
	srcInfo, err := srcFile.Stat()
	if err != nil {
		return 0, err
	}
	if srcInfo.IsDir() {
		return 0, errors.New("src is a directory")
	}
	// Ensure the destination directory exists before creating the file.
	dirPath := filepath.Dir(dest)
	if exists, _ := pathExists(dirPath); !exists {
		log.Println("mkdir all:", dirPath)
		if err := os.MkdirAll(dirPath, os.ModePerm); err != nil {
			return 0, err
		}
	}

	tmpFile, err := os.CreateTemp(dirPath, filepath.Base(dest)+".tmp-*")
	if err != nil {
		return 0, err
	}
	tmpPath := tmpFile.Name()
	cleanupTmp := true
	defer func() {
		_ = tmpFile.Close()
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()
	if !common.IsWindows() {
		if chmodErr := tmpFile.Chmod(srcInfo.Mode()); chmodErr != nil {
			return 0, chmodErr
		}
	}
	n, err := io.Copy(tmpFile, srcFile)
	if err != nil {
		return n, err
	}
	if syncErr := tmpFile.Sync(); syncErr != nil {
		return n, syncErr
	}
	if closeErr := tmpFile.Close(); closeErr != nil {
		return n, closeErr
	}
	if common.IsWindows() {
		if removeErr := os.Remove(dest); removeErr != nil && !os.IsNotExist(removeErr) {
			return n, removeErr
		}
	}
	if renameErr := os.Rename(tmpPath, dest); renameErr != nil {
		return n, renameErr
	}
	cleanupTmp = false
	return n, nil
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func chMod(name string, mode os.FileMode) {
	if !common.IsWindows() {
		_ = os.Chmod(name, mode)
	}
}
