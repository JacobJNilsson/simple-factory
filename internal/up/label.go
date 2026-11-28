package up

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/JacobJNilsson/simple-factory/internal/config"
)

// maxLabelLength is the most characters that a label holds, so it fits in a status header and a web page's factory list.
const maxLabelLength = 40

// InvalidLabelError reports a label text that is longer than maxLabelLength characters, is no valid UTF-8, or holds a control character.
type InvalidLabelError struct{ Text string }

func (e *InvalidLabelError) Error() string {
	return fmt.Sprintf("the label %q is not 1 to %d characters of valid UTF-8 with no control characters", e.Text, maxLabelLength)
}

// Label sets the display label of the factory that config.Home finds, as sfy label <text> does.
// An empty text removes the label. A text that is no valid label gives an InvalidLabelError and changes nothing.
func Label(env func(string) string, stdout io.Writer, text string) error {
	text = strings.TrimSpace(text)
	if !validLabel(text) {
		return &InvalidLabelError{Text: text}
	}
	home := config.Home(env)
	name, _ := config.Describe(home, env)
	path := filepath.Join(home, config.LabelFile)
	if text == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		_, err := fmt.Fprintf(stdout, "Factory %s has no label.\n", name)
		return err
	}
	if err := os.WriteFile(path, []byte(text+"\n"), 0o600); err != nil {
		return err
	}
	// WriteFile keeps the mode of a file that exists, so the label file gets its mode here.
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	_, err := fmt.Fprintf(stdout, "Factory %s is shown as %s.\n", name, text)
	return err
}

// validLabel reports whether text may be a label. The empty text is valid, because it removes the label.
func validLabel(text string) bool {
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) > maxLabelLength {
		return false
	}
	return !strings.ContainsFunc(text, unicode.IsControl)
}

// Factories prints one line for each factory folder in the factories folder: its name, its label or "-", and its working folder.
// The fields are tab-separated and the lines are in name order. It skips links and folders that record no working folder.
func Factories(env func(string) string, stdout io.Writer) error {
	dir := config.FactoriesDir(env("HOME"))
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		home := filepath.Join(dir, e.Name())
		folder := config.WorkFolder(home)
		if folder == "" {
			continue
		}
		label := config.LabelOf(home)
		if label == "" {
			label = "-"
		}
		if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\n", e.Name(), label, folder); err != nil {
			return err
		}
	}
	return nil
}
