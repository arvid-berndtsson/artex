package server

import (
	"archive/zip"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
)

const (
	maxSkillExportEntries    = 20000
	maxSkillExportTotalBytes = 512 << 20 // 512MB across the library
	maxSkillExportFileBytes  = 100 << 20 // 100MB per file
)

type skillExportBudget struct {
	entries int
	bytes   int64
}

func (b *skillExportBudget) addEntry(size int64) error {
	b.entries++
	if b.entries > maxSkillExportEntries {
		return fmt.Errorf("skill library contains more than %d files and folders", maxSkillExportEntries)
	}
	if size < 0 || size > maxSkillExportFileBytes {
		return fmt.Errorf("skill file exceeds the %dMB export limit", maxSkillExportFileBytes>>20)
	}
	b.bytes += size
	if b.bytes > maxSkillExportTotalBytes {
		return fmt.Errorf("skill library exceeds the %dMB export limit", maxSkillExportTotalBytes>>20)
	}
	return nil
}

// fsExportSkills streams every skill directory as a zip archive with the
// directory tree, file contents, and executable bits preserved.
func (s *Server) fsExportSkills(w http.ResponseWriter, r *http.Request) {
	if err := os.MkdirAll(s.skillDir, 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	rootEntries, err := os.ReadDir(s.skillDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	tmp, err := os.CreateTemp("", "artex-skills-*.zip")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	zw := zip.NewWriter(tmp)
	budget := &skillExportBudget{}
	skillCount := 0
	for _, entry := range rootEntries {
		if !entry.IsDir() || !validSkillName(entry.Name()) {
			continue
		}
		skillRoot := filepath.Join(s.skillDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			_ = zw.Close()
			_ = tmp.Close()
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := budget.addEntry(0); err != nil {
			_ = zw.Close()
			_ = tmp.Close()
			writeErr(w, http.StatusRequestEntityTooLarge, err.Error())
			return
		}
		if _, err := addSkillExportEntry(zw, entry.Name()+"/", info, nil); err != nil {
			_ = zw.Close()
			_ = tmp.Close()
			writeErr(w, http.StatusRequestEntityTooLarge, err.Error())
			return
		}
		if err := addSkillExportTree(zw, skillRoot, entry.Name(), budget); err != nil {
			_ = zw.Close()
			_ = tmp.Close()
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		skillCount++
	}
	if skillCount == 0 {
		_ = zw.Close()
		_ = tmp.Close()
		writeErr(w, http.StatusNotFound, "No skills are available to export")
		return
	}
	if err := zw.Close(); err != nil {
		_ = tmp.Close()
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	stat, err := tmp.Stat()
	if err != nil {
		_ = tmp.Close()
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		_ = tmp.Close()
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="artex-skills.zip"`)
	w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	w.Header().Set("Cache-Control", "no-store")
	if _, err := io.Copy(w, tmp); err != nil {
		log.Printf("[skills] export response interrupted: %v", err)
	}
	_ = tmp.Close()
}

func addSkillExportTree(zw *zip.Writer, skillRoot, skillName string, budget *skillExportBudget) error {
	return filepath.WalkDir(skillRoot, func(filename string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("cannot read %s: %w", filename, walkErr)
		}
		if filename == skillRoot {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("cannot export symbolic link: %s", filename)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("cannot inspect %s: %w", filename, err)
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("cannot export non-regular file: %s", filename)
		}

		rel, err := filepath.Rel(skillRoot, filename)
		if err != nil {
			return err
		}
		rel, msg := skillRelPath(filepath.ToSlash(rel))
		if msg != "" {
			return fmt.Errorf("cannot export unsafe path %s: %s", filename, msg)
		}
		archiveName := path.Join(skillName, filepath.ToSlash(rel))
		var source *os.File
		var file io.Reader
		if !info.IsDir() {
			if err := budget.addEntry(info.Size()); err != nil {
				return err
			}
			source, err = os.Open(filename)
			if err != nil {
				return fmt.Errorf("cannot open %s: %w", filename, err)
			}
			file = io.LimitReader(source, maxSkillExportFileBytes+1)
		} else if err := budget.addEntry(0); err != nil {
			return err
		}
		if info.IsDir() {
			archiveName += "/"
		}
		n, err := addSkillExportEntry(zw, archiveName, info, file)
		if err != nil {
			if source != nil {
				_ = source.Close()
			}
			return fmt.Errorf("cannot archive %s: %w", filename, err)
		}
		if source != nil {
			if err := source.Close(); err != nil {
				return err
			}
			if n != info.Size() {
				return fmt.Errorf("file changed while exporting: %s", filename)
			}
		}
		return nil
	})
}

func addSkillExportEntry(zw *zip.Writer, name string, info os.FileInfo, file io.Reader) (int64, error) {
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return 0, err
	}
	header.Name = name
	header.Method = zip.Deflate
	header.SetMode(info.Mode())
	writer, err := zw.CreateHeader(header)
	if err != nil {
		return 0, err
	}
	if file == nil {
		return 0, nil
	}
	return io.Copy(writer, file)
}
