package templates

import (
	"bytes"
	"errors"
	"net/mail"
	"os"
	"path"
	"strings"
	"text/template"
	"time"

	"github.com/mitchellh/go-homedir"
)

type TemplateData struct {
	To      []*mail.Address
	Cc      []*mail.Address
	Bcc     []*mail.Address
	From    []*mail.Address
	Date    time.Time
	Subject string
	// Only available when replying with a quote
	OriginalText string
	OriginalFrom []*mail.Address
	OriginalDate time.Time
}

func TestTemplateData() TemplateData {
	defaults := map[string]string{
		"To":           "John Doe <john@example.com>",
		"Cc":           "Josh Doe <josh@example.com>",
		"From":         "Jane Smith <jane@example.com>",
		"Subject":      "This is only a test",
		"OriginalText": "This is only a test text",
		"OriginalFrom": "John Doe <john@example.com>",
		"OriginalDate": time.Now().Format("Mon Jan 2, 2006 at 3:04 PM"),
	}

	return ParseTemplateData(defaults)
}

func ParseTemplateData(defaults map[string]string) TemplateData {
	originalDate, _ := time.Parse("Mon Jan 2, 2006 at 3:04 PM", defaults["OriginalDate"])
	td := TemplateData{
		To:           parseAddressList(defaults["To"]),
		Cc:           parseAddressList(defaults["Cc"]),
		Bcc:          parseAddressList(defaults["Bcc"]),
		From:         parseAddressList(defaults["From"]),
		Date:         time.Now(),
		Subject:      defaults["Subject"],
		OriginalText: defaults["Original"],
		OriginalFrom: parseAddressList(defaults["OriginalFrom"]),
		OriginalDate: originalDate,
	}
	return td
}

func parseAddressList(list string) []*mail.Address {
	addrs, err := mail.ParseAddressList(list)
	if err != nil {
		return nil
	}

	return addrs
}

func wrapLine(text string, lineWidth int) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return text
	}
	var wrapped strings.Builder
	wrapped.WriteString(words[0])
	spaceLeft := lineWidth - wrapped.Len()
	for _, word := range words[1:] {
		if len(word)+1 > spaceLeft {
			wrapped.WriteRune('\n')
			wrapped.WriteString(word)
			spaceLeft = lineWidth - len(word)
		} else {
			wrapped.WriteRune(' ')
			wrapped.WriteString(word)
			spaceLeft -= 1 + len(word)
		}
	}

	return wrapped.String()
}

func wrapText(text string, lineWidth int) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	var wrapped strings.Builder

	for _, line := range lines {
		switch {
		case line == "":
			// deliberately left blank
		case line[0] == '>':
			// leave quoted text alone
			wrapped.WriteString(line)
		default:
			wrapped.WriteString(wrapLine(line, lineWidth))
		}
		wrapped.WriteRune('\n')
	}
	return wrapped.String()
}

// quote prepends "> " in front of every line in text
func quote(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	var quoted strings.Builder
	for _, line := range lines {
		if line == "" {
			quoted.WriteString(">\n")
		}
		quoted.WriteString("> ")
		quoted.WriteString(line)
		quoted.WriteRune('\n')
	}

	return quoted.String()
}

var templateFuncs = template.FuncMap{
	"quote":      quote,
	"wrapText":   wrapText,
	"dateFormat": time.Time.Format,
}

func findTemplate(templateName string, templateDirs []string) (string, error) {
	for _, dir := range templateDirs {
		templateFile, err := homedir.Expand(path.Join(dir, templateName))
		if err != nil {
			return "", err
		}

		if _, err := os.Stat(templateFile); os.IsNotExist(err) {
			continue
		}
		return templateFile, nil
	}

	return "", errors.New("Can't find template - " + templateName)
}

func ParseTemplateFromFile(templateName string, templateDirs []string, data interface{}) ([]byte, error) {
	templateFile, err := findTemplate(templateName, templateDirs)
	if err != nil {
		return nil, err
	}
	emailTemplate, err := template.New(templateName).
		Funcs(templateFuncs).ParseFiles(templateFile)
	if err != nil {
		return nil, err
	}

	var outString bytes.Buffer
	if err := emailTemplate.Execute(&outString, data); err != nil {
		return nil, err
	}
	return outString.Bytes(), nil
}

func ParseTemplate(templateText string, data interface{}) ([]byte, error) {
	emailTemplate, err :=
		template.New("email_template").Funcs(templateFuncs).Parse(templateText)
	if err != nil {
		return nil, err
	}

	var outString bytes.Buffer
	if err := emailTemplate.Execute(&outString, data); err != nil {
		return nil, err
	}
	return outString.Bytes(), nil
}