package logger

import (
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
)

// LogConfig defines the configuration for logging
// Level defines the log level - Warn, Info, Debug
// LogFile defines the location of the log file. Yet to implement.
type LogConfig struct {
	Level   log.Level
	LogFile string
}

// New returns a new instance of the logger which is used throughout the application.
func New(cfg LogConfig) *log.Logger {
	styles := log.DefaultStyles()

	makeStyle := func(fg string, bg string, bold bool, padLeftRight int) lipgloss.Style {
		s := lipgloss.NewStyle()
		if fg != "" {
			s = s.Foreground(lipgloss.Color(fg))
		}
		if bg != "" {
			s = s.Background(lipgloss.Color(bg))
		}
		if bold {
			s = s.Bold(true)
		}
		if padLeftRight > 0 {
			s = s.Padding(0, padLeftRight, 0, padLeftRight)
		}
		return s
	}

	styles.Levels[log.DebugLevel] = makeStyle("#ffffff", "#8A79FF", false, 1).SetString(" DEBUG ")
	styles.Levels[log.InfoLevel] = makeStyle("#000000", "#2DA4FF", false, 1).SetString(" INFO  ")
	styles.Levels[log.WarnLevel] = makeStyle("#000000", "#FFB020", false, 1).SetString(" WARN  ")
	styles.Levels[log.ErrorLevel] = makeStyle("#000000", "#FF5C57", true, 1).SetString(" ERROR ")
	styles.Levels[log.FatalLevel] = makeStyle("#ffffff", "#D60B0B", true, 1).SetString(" FATAL ")

	styles.Timestamp = lipgloss.NewStyle().Foreground(lipgloss.Color("#99A0A6"))
	styles.Prefix = lipgloss.NewStyle().Foreground(lipgloss.Color("#99A0A6")).Bold(false)

	// adding this for the actual text colors of the error/fatal logs
	styles.Keys["err"] = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	styles.Values["err"] = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)

	logger := log.NewWithOptions(os.Stderr, log.Options{
		ReportCaller:    true,
		Level:           cfg.Level,
		ReportTimestamp: true,
		TimeFormat:      "15:04:05",
		Prefix:          "databasemanager",
	})

	logger.SetStyles(styles)

	return logger
}
