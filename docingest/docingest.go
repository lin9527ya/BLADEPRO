// Package docingest 把文档/文本提取成可入库的 Markdown 文本（移植自 quiver，AGPL-3.0）。
//
// 支持：md/txt/json/csv 直通；.docx 用 zip+XML 提取段落；.pdf 调外部 pdftotext -layout。
// 上限 2 MiB（文本）与 32 MiB（压缩包），解压总量同样受限，防 zip 炸弹。
package docingest

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	// MaxTextBytes 是提取后文本的上限。
	MaxTextBytes = 2 << 20
	// MaxArchiveBytes 是输入压缩包的上限。
	MaxArchiveBytes = 32 << 20
	// maxUnzipBytes 是 docx 解压总量上限。
	maxUnzipBytes = 64 << 20
)

// Extract 按文件名后缀提取文本；未知类型按文本直通处理。
func Extract(filename string, data []byte) (string, error) {
	if len(data) > MaxArchiveBytes {
		return "", fmt.Errorf("文件过大（%d 字节，上限 %d）", len(data), MaxArchiveBytes)
	}
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".docx":
		return extractDocx(data)
	case ".pdf":
		return extractPDF(filename, data)
	case ".md", ".markdown", ".txt", ".json", ".csv", ".yaml", ".yml", ".html", ".htm", "":
		return clipText(string(data)), nil
	default:
		// 未知类型：如果能当 UTF-8 文本读就用，否则提示不支持。
		s := string(data)
		if strings.ContainsRune(s, 0) {
			return "", fmt.Errorf("不支持的文件类型 %q（支持 md/txt/json/csv/docx/pdf）", filepath.Ext(filename))
		}
		return clipText(s), nil
	}
}

func clipText(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > MaxTextBytes {
		s = s[:MaxTextBytes]
	}
	return s
}

// extractDocx 解析 word/document.xml，把段落拼成 Markdown（不做样式还原）。
func extractDocx(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("docx 解析失败：%w", err)
	}
	var total int64
	var doc *zip.File
	for _, f := range zr.File {
		total += int64(f.UncompressedSize64)
		if total > maxUnzipBytes {
			return "", fmt.Errorf("docx 解压后过大（防 zip 炸弹）")
		}
		if f.Name == "word/document.xml" {
			doc = f
		}
	}
	if doc == nil {
		return "", fmt.Errorf("docx 里没有 word/document.xml")
	}
	rc, err := doc.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	dec := xml.NewDecoder(io.LimitReader(rc, maxUnzipBytes))
	var b strings.Builder
	inText := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("docx XML 解析失败：%w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inText = true
			case "p":
				if b.Len() > 0 {
					b.WriteString("\n\n")
				}
			case "br":
				b.WriteString("\n")
			}
		case xml.EndElement:
			if t.Name.Local == "t" {
				inText = false
			}
		case xml.CharData:
			if inText {
				b.Write(t)
			}
		}
		if b.Len() > MaxTextBytes {
			break
		}
	}
	return clipText(b.String()), nil
}

// extractPDF 调系统 pdftotext（poppler）。缺失时给出明确安装提示。
func extractPDF(filename string, data []byte) (string, error) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		return "", fmt.Errorf("缺少 pdftotext（poppler）：安装后重试，或先转成文本再入库")
	}
	cmd := exec.Command("pdftotext", "-layout", "-enc", "UTF-8", "-", "-")
	cmd.Stdin = bytes.NewReader(data)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pdftotext 失败：%v（%s）", err, strings.TrimSpace(errBuf.String()))
	}
	return clipText(out.String()), nil
}
