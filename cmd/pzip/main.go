package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/MattInnovates/Project-Zipper/internal/zipper"
	"golang.org/x/sys/windows/registry"
)

func main() {
	extractFlag := flag.Bool("x", false, "extract mode: extract archive to destination")
	formatFlag := flag.String("f", "zip", "archive format: zip or gz (tar.gz)")
	contextFlag := flag.String("context", "", "install/uninstall Windows context menu: install, uninstall, or status")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [options] <source> [destination]\n", filepath.Base(os.Args[0]))
		fmt.Fprintln(flag.CommandLine.Output(), "\nCreate or extract archives.")
		fmt.Fprintln(flag.CommandLine.Output(), "CREATE MODE (default):")
		fmt.Fprintln(flag.CommandLine.Output(), "  pz <folder>           Create a zip archive of the folder")
		fmt.Fprintln(flag.CommandLine.Output(), "  pz -f gz <folder>     Create a tar.gz archive of the folder")
		fmt.Fprintln(flag.CommandLine.Output(), "\nEXTRACT MODE:")
		fmt.Fprintln(flag.CommandLine.Output(), "  pz -x <archive.zip>   Extract archive to current directory")
		fmt.Fprintln(flag.CommandLine.Output(), "  pz -x <archive.tar.gz> <dest>  Extract archive to destination folder")
		fmt.Fprintln(flag.CommandLine.Output(), "\nCONTEXT MENU (Windows):")
		fmt.Fprintln(flag.CommandLine.Output(), "  pz --context install    Add 'Compress with pz' to Windows context menu")
		fmt.Fprintln(flag.CommandLine.Output(), "  pz --context uninstall  Remove from Windows context menu")
		fmt.Fprintln(flag.CommandLine.Output(), "  pz --context status     Check installation status")
	}

	flag.Parse()

	// Handle context menu operations
	if *contextFlag != "" {
		handleContextMenu(*contextFlag)
		return
	}

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}

	if *extractFlag {
		doExtract(flag.Args())
	} else {
		doCreate(flag.Args(), *formatFlag)
	}
}

func doCreate(args []string, format string) {
	target := strings.Join(args, " ")
	absTarget, err := filepath.Abs(target)
	if err != nil {
		exitWithError(err)
	}

	info, err := os.Stat(absTarget)
	if err != nil {
		exitWithError(err)
	}
	if !info.IsDir() {
		exitWithError(errors.New("target must be a directory"))
	}

	parent := filepath.Dir(absTarget)
	base := filepath.Base(absTarget)

	var archivePath string
	var stats zipper.ArchiveStats

	printer := newCreateProgressPrinter(absTarget)

	switch strings.ToLower(format) {
	case "gz", "gzip", "tar.gz":
		archivePath, err = zipper.NextGzipArchiveName(parent, base)
		if err != nil {
			exitWithError(err)
		}
		stats, err = zipper.GzipWithProgressAndFile(absTarget, archivePath, printer.OnProgressWithFile)
		if err != nil {
			exitWithError(err)
		}
	case "zip":
		archivePath, err = zipper.NextArchiveName(parent, base)
		if err != nil {
			exitWithError(err)
		}
		stats, err = zipper.ZipWithProgressAndFile(absTarget, archivePath, printer.OnProgressWithFile)
		if err != nil {
			exitWithError(err)
		}
	default:
		exitWithError(fmt.Errorf("unsupported format: %s (use 'zip' or 'gz')", format))
	}

	printer.Complete(archivePath, stats)
	fmt.Println(archivePath)
}

func doExtract(args []string) {
	if len(args) < 1 {
		exitWithError(errors.New("extract mode requires an archive file"))
	}

	archivePath := strings.Join(args, " ")
	if len(args) > 1 {
		// If multiple args, first is archive, rest is destination
		archivePath = args[0]
	}

	absArchivePath, err := filepath.Abs(archivePath)
	if err != nil {
		exitWithError(err)
	}

	info, err := os.Stat(absArchivePath)
	if err != nil {
		exitWithError(err)
	}
	if info.IsDir() {
		exitWithError(errors.New("source must be an archive file, not a directory"))
	}

	// Determine destination
	var destDir string
	if len(args) > 1 {
		destDir = strings.Join(args[1:], " ")
	} else {
		// Extract to current directory
		destDir, err = os.Getwd()
		if err != nil {
			exitWithError(err)
		}
	}

	absDestDir, err := filepath.Abs(destDir)
	if err != nil {
		exitWithError(err)
	}

	printer := newExtractProgressPrinter(absArchivePath, absDestDir)

	// Auto-detect format based on file extension
	var stats zipper.ExtractStats
	if strings.HasSuffix(strings.ToLower(absArchivePath), ".tar.gz") || strings.HasSuffix(strings.ToLower(absArchivePath), ".tgz") {
		stats, err = zipper.ExtractGzipWithProgress(absArchivePath, absDestDir, printer.OnProgress)
	} else if strings.HasSuffix(strings.ToLower(absArchivePath), ".gz") {
		// Check if it's a tar.gz by trying to open as such
		stats, err = zipper.ExtractGzipWithProgress(absArchivePath, absDestDir, printer.OnProgress)
	} else {
		// Default to zip
		stats, err = zipper.ExtractWithProgress(absArchivePath, absDestDir, printer.OnProgress)
	}

	if err != nil {
		exitWithError(err)
	}
	printer.Complete(stats)

	fmt.Println(absDestDir)
}

func exitWithError(err error) {
	fmt.Fprintln(os.Stderr, "pz:", err)
	os.Exit(1)
}

// Create mode progress printer
type createProgressPrinter struct {
	source      string
	started     bool
	startTime   time.Time
	total       int64
	lastLen     int
	currentFile string
}

func newCreateProgressPrinter(source string) *createProgressPrinter {
	return &createProgressPrinter{source: source}
}

func (p *createProgressPrinter) OnProgress(done, total int64) {
	if !p.started {
		p.started = true
		p.startTime = time.Now()
		p.total = total
		numCPU := runtime.NumCPU()
		workers := numCPU / 5
		if workers < 1 {
			workers = 1
		}
		fmt.Fprintf(os.Stdout, "[%s] Creating archive for %s (%s) using %d/%d CPUs...\n", p.startTime.Format("15:04:05"), p.source, formatBytes(total), workers, numCPU)
	}

	line := p.renderLine(done, total)
	p.printLine(line)
}

func (p *createProgressPrinter) OnProgressWithFile(done, total int64, currentFile string) {
	p.currentFile = currentFile
	p.OnProgress(done, total)
}

func (p *createProgressPrinter) renderLine(done, total int64) string {
	const barWidth = 50

	filled := 0
	percent := 100.0
	if total > 0 {
		percent = (float64(done) / float64(total)) * 100
		if percent < 0 {
			percent = 0
		}
		if percent > 100 {
			percent = 100
		}
		filled = int((done * int64(barWidth)) / total)
	} else {
		// Empty directory; treat as complete.
		filled = barWidth
	}
	if filled > barWidth {
		filled = barWidth
	}

	bar := strings.Repeat("#", filled) + strings.Repeat("-", barWidth-filled)
	selapsed := time.Since(p.startTime)
	speed := "0 B/s"
	if selapsed > 0 {
		bytesPerSec := float64(done) / selapsed.Seconds()
		if bytesPerSec < 0 {
			bytesPerSec = 0
		}
		speedValue := int64(bytesPerSec + 0.5)
		speed = fmt.Sprintf("%s/s", formatBytes(speedValue))
	}

	return fmt.Sprintf("[%s] %3.0f%% (%s/%s) %s", bar, percent, formatBytes(done), formatBytes(total), speed)
}

func (p *createProgressPrinter) printLine(line string) {
	// Move cursor up if we printed file line before
	if p.currentFile != "" && p.lastLen > 0 {
		fmt.Print("\033[2K\r\033[1A\033[2K\r") // Clear current line, move up, clear that line
	} else if p.lastLen > 0 {
		fmt.Print("\r") // Just return to start of line
	}

	// Print progress bar
	fmt.Print(line)

	// Print current file on same line if available
	if p.currentFile != "" {
		const maxFileLen = 50
		displayFile := p.currentFile
		if len(displayFile) > maxFileLen {
			displayFile = "..." + displayFile[len(displayFile)-maxFileLen+3:]
		}
		fileLine := fmt.Sprintf("\n%s", displayFile)
		fmt.Print(fileLine)
	}

	p.lastLen = len(line)
}

func (p *createProgressPrinter) Complete(zipPath string, stats zipper.ArchiveStats) {
	if !p.started {
		fmt.Println("No files to archive; created empty zip.")
		return
	}
	fmt.Print("\n")
	p.lastLen = 0
	zipInfo, err := os.Stat(zipPath)
	zipSize := int64(0)
	if err == nil {
		zipSize = zipInfo.Size()
	}
	elapsed := time.Since(p.startTime)
	fmt.Fprintf(os.Stdout, "✓ Archive complete: %s -> %s (%s source, %s archive, %d files, %s)\n",
		p.source,
		zipPath,
		formatBytes(stats.TotalBytes),
		formatBytes(zipSize),
		stats.FileCount,
		formatDuration(elapsed),
	)
	if stats.Checksum != "" {
		fmt.Fprintf(os.Stdout, "  SHA-256: %s\n", stats.Checksum)
	}
}

// Extract mode progress printer
type extractProgressPrinter struct {
	zipPath   string
	destDir   string
	started   bool
	startTime time.Time
	total     int64
	lastLen   int
}

func newExtractProgressPrinter(zipPath, destDir string) *extractProgressPrinter {
	return &extractProgressPrinter{
		zipPath: zipPath,
		destDir: destDir,
	}
}

func (p *extractProgressPrinter) OnProgress(done, total int64) {
	if !p.started {
		p.started = true
		p.startTime = time.Now()
		p.total = total
		numCPU := runtime.NumCPU()
		workers := numCPU / 5
		if workers < 1 {
			workers = 1
		}
		fmt.Fprintf(os.Stdout, "[%s] Extracting %s (%s) using %d/%d CPUs...\n", p.startTime.Format("15:04:05"), filepath.Base(p.zipPath), formatBytes(total), workers, numCPU)
	}

	line := p.renderLine(done, total)
	p.printLine(line)
}

func (p *extractProgressPrinter) renderLine(done, total int64) string {
	const barWidth = 50

	filled := 0
	percent := 100.0
	if total > 0 {
		percent = (float64(done) / float64(total)) * 100
		if percent < 0 {
			percent = 0
		}
		if percent > 100 {
			percent = 100
		}
		filled = int((done * int64(barWidth)) / total)
	} else {
		filled = barWidth
	}
	if filled > barWidth {
		filled = barWidth
	}

	bar := strings.Repeat("#", filled) + strings.Repeat("-", barWidth-filled)
	selapsed := time.Since(p.startTime)
	speed := "0 B/s"
	if selapsed > 0 {
		bytesPerSec := float64(done) / selapsed.Seconds()
		if bytesPerSec < 0 {
			bytesPerSec = 0
		}
		speedValue := int64(bytesPerSec + 0.5)
		speed = fmt.Sprintf("%s/s", formatBytes(speedValue))
	}

	return fmt.Sprintf("[%s] %3.0f%% (%s/%s) %s", bar, percent, formatBytes(done), formatBytes(total), speed)
}

func (p *extractProgressPrinter) printLine(line string) {
	if pad := p.lastLen - len(line); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	fmt.Printf("\r%s", line)
	p.lastLen = len(line)
}

func (p *extractProgressPrinter) Complete(stats zipper.ExtractStats) {
	if !p.started {
		fmt.Println("No files extracted.")
		return
	}
	fmt.Print("\n")
	p.lastLen = 0
	elapsed := time.Since(p.startTime)
	fmt.Fprintf(os.Stdout, "✓ Extraction complete: %s -> %s (%s extracted, %d files, %s)\n",
		filepath.Base(p.zipPath),
		p.destDir,
		formatBytes(stats.TotalBytes),
		stats.FileCount,
		formatDuration(elapsed),
	)
}

type progressPrinter = createProgressPrinter

func newProgressPrinter(source string) *progressPrinter {
	return newCreateProgressPrinter(source)
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}

	suffixes := []string{"KB", "MB", "GB", "TB", "PB"}
	div := float64(unit)
	exp := 0
	for n/int64(div) >= unit && exp < len(suffixes)-1 {
		div *= unit
		exp++
	}
	value := float64(n) / div
	return fmt.Sprintf("%.1f %s", value, suffixes[exp])
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	seconds := int(d.Seconds())
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	seconds = seconds % 60
	if minutes < 60 {
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	}
	hours := minutes / 60
	minutes = minutes % 60
	return fmt.Sprintf("%dh%dm", hours, minutes)
}

// handleContextMenu manages Windows context menu integration
func handleContextMenu(action string) {
	if runtime.GOOS != "windows" {
		fmt.Fprintln(os.Stderr, "Context menu integration is only available on Windows")
		os.Exit(1)
	}

	switch strings.ToLower(action) {
	case "install":
		if err := installContextMenu(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to install context menu: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✓ Context menu installed successfully!")
		fmt.Println("Right-click any folder or file and look for 'Compress with pz' options")
	case "uninstall":
		if err := uninstallContextMenu(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to uninstall context menu: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✓ Context menu uninstalled successfully!")
	case "status":
		checkContextMenuStatus()
	default:
		fmt.Fprintf(os.Stderr, "Unknown action: %s (use install, uninstall, or status)\n", action)
		os.Exit(1)
	}
}

// installContextMenu adds registry entries for Windows Explorer context menu
func installContextMenu() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot get executable path: %w", err)
	}

	// Check if running as administrator
	if !isAdmin() {
		fmt.Println("⚠ Administrator privileges required for context menu installation")
		fmt.Println("Attempting to restart with administrator privileges...")
		return runAsAdmin("--context", "install")
	}

	// Directory background context menu (right-click in folder)
	keys := []struct {
		path    string
		command string
		name    string
	}{
		{
			path:    `Directory\\shell\\pz_zip`,
			command: fmt.Sprintf(`"%s" "%%V"`, exePath),
			name:    "Compress to ZIP",
		},
		{
			path:    `Directory\\shell\\pz_targz`,
			command: fmt.Sprintf(`"%s" -f gz "%%V"`, exePath),
			name:    "Compress to tar.gz",
		},
		{
			path:    `Directory\\Background\\shell\\pz_zip`,
			command: fmt.Sprintf(`"%s" "%%V"`, exePath),
			name:    "Compress folder to ZIP",
		},
		{
			path:    `Directory\\Background\\shell\\pz_targz`,
			command: fmt.Sprintf(`"%s" -f gz "%%V"`, exePath),
			name:    "Compress folder to tar.gz",
		},
		{
			path:    `*\\shell\\pz_zip`,
			command: fmt.Sprintf(`"%s" "%%1"`, exePath),
			name:    "Compress to ZIP",
		},
		{
			path:    `*\\shell\\pz_extract`,
			command: fmt.Sprintf(`"%s" -x "%%1"`, exePath),
			name:    "Extract here",
		},
	}

	for _, k := range keys {
		key, _, err := registry.CreateKey(registry.CLASSES_ROOT, k.path, registry.SET_VALUE)
		if err != nil {
			return fmt.Errorf("failed to create key %s: %w", k.path, err)
		}
		if err := key.SetStringValue("", k.name); err != nil {
			key.Close()
			return fmt.Errorf("failed to set name for %s: %w", k.path, err)
		}
		key.Close()

		// Set icon
		iconKey, _, err := registry.CreateKey(registry.CLASSES_ROOT, k.path, registry.SET_VALUE)
		if err == nil {
			iconKey.SetStringValue("Icon", exePath+",0")
			iconKey.Close()
		}

		// Create command subkey
		cmdKey, _, err := registry.CreateKey(registry.CLASSES_ROOT, k.path+`\\command`, registry.SET_VALUE)
		if err != nil {
			return fmt.Errorf("failed to create command key for %s: %w", k.path, err)
		}
		if err := cmdKey.SetStringValue("", k.command); err != nil {
			cmdKey.Close()
			return fmt.Errorf("failed to set command for %s: %w", k.path, err)
		}
		cmdKey.Close()
	}

	return nil
}

// uninstallContextMenu removes registry entries
func uninstallContextMenu() error {
	// Check if running as administrator
	if !isAdmin() {
		fmt.Println("⚠ Administrator privileges required for context menu uninstallation")
		fmt.Println("Attempting to restart with administrator privileges...")
		return runAsAdmin("--context", "uninstall")
	}

	keys := []string{
		`Directory\\shell\\pz_zip`,
		`Directory\\shell\\pz_targz`,
		`Directory\\Background\\shell\\pz_zip`,
		`Directory\\Background\\shell\\pz_targz`,
		`*\\shell\\pz_zip`,
		`*\\shell\\pz_extract`,
	}

	var errors []string
	for _, k := range keys {
		if err := registry.DeleteKey(registry.CLASSES_ROOT, k); err != nil {
			if err != registry.ErrNotExist {
				errors = append(errors, fmt.Sprintf("%s: %v", k, err))
			}
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("some keys could not be removed:\\n%s", strings.Join(errors, "\\n"))
	}

	return nil
}

// checkContextMenuStatus checks if context menu is installed
func checkContextMenuStatus() {
	key, err := registry.OpenKey(registry.CLASSES_ROOT, `Directory\\shell\\pz_zip`, registry.QUERY_VALUE)
	if err == nil {
		key.Close()
		fmt.Println("✓ Context menu is installed")

		exePath, _ := os.Executable()
		cmdKey, err := registry.OpenKey(registry.CLASSES_ROOT, `Directory\\shell\\pz_zip\\command`, registry.QUERY_VALUE)
		if err == nil {
			cmd, _, _ := cmdKey.GetStringValue("")
			cmdKey.Close()
			fmt.Printf("  Executable: %s\n", exePath)
			fmt.Printf("  Command: %s\n", cmd)
		}
	} else {
		fmt.Println("✗ Context menu is not installed")
		fmt.Println("  Run: pz --context install")
	}
}

// isAdmin checks if the current process has administrator privileges
func isAdmin() bool {
	_, err := os.Open("\\\\.\\PHYSICALDRIVE0")
	return err == nil
}

// runAsAdmin restarts the program with administrator privileges
func runAsAdmin(args ...string) error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	verb := "runas"
	cmd := exec.Command("powershell", "-Command", "Start-Process", "-Verb", verb, "-FilePath", exePath, "-ArgumentList", strings.Join(args, ","), "-Wait")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to elevate privileges: %w", err)
	}

	os.Exit(0)
	return nil
}
