package main

import (
	"github.com/alecthomas/kingpin"
	"os"
	"io/ioutil"
	"fmt"
	"strings"
	re "regexp"
	"time"
	"io"
	"path"
	"os/exec"
)

var (
	backup = kingpin.Flag("backup", "Create a backup file including the timestamp information so you can get the"+
		" original file back if you somehow clobbered it incorrectly.").Default("false").Bool()
	create = kingpin.Flag("create", "If specified, the file will be created if it does"+
		" not already exist. By default it will fail if the file is missing.").Default("false").Bool()
	insert_after = kingpin.Flag("insert-after", "If specified, the line will"+
		" be inserted after the last match of specified regular expression. A special value is available; EOF"+
		"for inserting the line at the end of the file. If specified regular expression has no matches, EOF"+
		" will be used instead.").Default("EOF").String()
	insert_before = kingpin.Flag("insert-before", "If specified, the line will be inserted"+
		" before the last match of specified regular expression. A value is available; BOF for inserting the"+
		" line at the beginning of the file. If specified regular expression has no matches, the line will be"+
		" inserted at the end of the file.").String()
	regexp = kingpin.Flag("regexp", "The regular expression to look for in every line of the file. For"+
		" state=present, the pattern to replace if found; only the last line found will be replaced. For"+
		" state=absent, the pattern of the line to remove. ").String()
	absent = kingpin.Flag("absent", "The line shouldn't be there").Default("false").Bool()
	//unsafe_writes = kingpin.Flag("unsafe-write", "Normally this module uses atomic operations to prevent data"+
	//	" corruption or inconsistent reads from the target files, sometimes systems are configured or just"+
	//	" broken in ways that prevent this. One example are docker mounted files, they cannot be updated"+
	//	" atomically and can only be done in an unsafe manner. This boolean option allows ansible to fall back"+
	//	" to unsafe methods of updating files for those cases in which you do not have any other choice. Be"+
	//	" aware that this is subject to race conditions and can lead to data corruption.").Default("false").Bool()
	group = kingpin.Flag("group", "Name of the group that should own the file/directory, as would be fed to chown.").String()
	mode = kingpin.Flag("mode", "mode the file or directory should be. For those used to /usr/bin/chmod remember"+
		" that modes are actually octal numbers (like 0644). Leaving off the leading zero will likely have"+
		" unexpected results.").String()
	owner = kingpin.Flag("owner", "name of the user that should own the file/directory, as would be fed to chown").String()
	line = kingpin.Arg("line", "The line to insert/replace into the file.").Required().String()
	dest   = kingpin.Arg("dest", "The file to modify.").Required().String()
)

func main() {
	kingpin.Version("0.1.0")
	kingpin.Parse()
	if *absent {
		do_absent()
	} else {
		do_present()
	}
}

func do_absent() error {
	if _, err := os.Stat(*dest); os.IsNotExist(err) {
		return nil
	}

	content, err := ioutil.ReadFile(*dest)
	if err != nil {
		fmt.Print(err)
		return err
	}
	b_lines := strings.Split(string(content), "\n")

	keep := make([]string, 0)
	if *regexp != "" {
		bre_c, err := re.Compile(*regexp)
		if err != nil {
			return err
		}
		for _, l := range(b_lines) {
			if bre_c.MatchString(l) {
				keep = append(keep, l)
			}
		}
	} else {
		for _, l := range(b_lines) {
			if l != *line {
				keep = append(keep, l)
			}
		}
	}

	if len(keep) != len(b_lines) {
		if *backup {
			if err := backup_local(*dest); err != nil {
				return err
			}
		}
	}

	return write_changes(keep)
}


func backup_local(fn string) error {
	ext := time.Now().Format(time.RFC3339)
	backupdest := fmt.Sprintf("%s.%d.%s", fn, os.Getpid(), ext)
	return copyFile(fn, backupdest)
}

func copyFile(src, dst string) (err error) {
    in, err := os.Open(src)
    if err != nil {
        return
    }
    defer in.Close()
    out, err := os.Create(dst)
    if err != nil {
        return
    }
    defer func() {
        cerr := out.Close()
        if err == nil {
            err = cerr
        }
    }()
    if _, err = io.Copy(out, in); err != nil {
        return
    }
    err = out.Sync()
    return
}


func write_changes(b_lines []string) error {
	tmpfile, err := ioutil.TempFile("", "lineinfile")
	if err != nil {
		return err
	}
	defer os.Remove(tmpfile.Name()) // clean up
	if _, err := tmpfile.WriteString(strings.Join(b_lines, "\n")); err != nil {
		return err
	}
	if err := tmpfile.Close(); err != nil {
		return err
	}

	// FIXME: validate
	if *owner != "" {
		if err := exec.Command("chown", *owner, tmpfile.Name()).Run(); err != nil {
			return err
		}
	}
	if *group != "" {
		if err := exec.Command("chgrp", *group, tmpfile.Name()).Run(); err != nil {
			return err
		}
	}
	if *mode != "" {
		if err := exec.Command("chmod", *mode, tmpfile.Name()).Run(); err != nil {
			return err
		}
	}
	if err := os.Rename(tmpfile.Name(), *dest); err != nil {
		return err
	}
	return nil
}

func do_present() error {
	var b_lines []string
	if _, err := os.Stat(*dest); os.IsNotExist(err) {
		if !*create {
			return err
		}
		b_destpath := path.Dir(*dest)
		if _, err := os.Stat(*dest); os.IsNotExist(err) {
			os.MkdirAll(b_destpath, 0511)
		}
	} else {
		content, err := ioutil.ReadFile(*dest)
		if err != nil {
			fmt.Print(err)
			return err
		}
		b_lines = strings.Split(string(content), "\n")
	}

	var bre_m *re.Regexp
	if *regexp != "" {
		res, err := re.Compile(*regexp)
		if err != nil {
			return err
		}
		bre_m = res
	}

	var bre_ins *re.Regexp
	if *insert_after != "" && *insert_after != "BOF" && *insert_after != "EOF" {
		res, err := re.Compile(*insert_after)
		if err != nil {
			return err
		}
		bre_ins = res
	} else 	if *insert_before != "" && *insert_before != "BOF" {
		res, err := re.Compile(*insert_before)
		if err != nil {
			return err
		}
		bre_ins = res
	}

	line_idx := -1
	insert_idx := -1
	for lineno, b_cur_line := range(b_lines) {
		match_found := false
		if *regexp != "" {
			match_found = bre_m.MatchString(b_cur_line)
		} else {
			match_found = b_cur_line == *line
		}
		if match_found {
			line_idx = lineno
		} else if bre_ins != nil && bre_ins.MatchString(b_cur_line) {
			if *insert_after != "" {
				insert_idx = lineno + 1
			}
			if *insert_before != "" {
				insert_idx = lineno
			}
		}
	}

	changed := false
	if line_idx != -1 {
		changed = b_lines[line_idx] != *line
		b_lines[line_idx] = *line
	} else if insert_idx != -1 {
		b_lines = insert(b_lines, insert_idx, *line)
		changed = true
	} else if *insert_before == "BOF" || *insert_after == "BOF" {
		b_lines = append([]string{*line}, b_lines...)
		changed = true
	} else {
		if len(b_lines) > 0 && b_lines[len(b_lines) - 1] == "" {
			b_lines = insert(b_lines, len(b_lines) - 1, *line)
		} else {
			b_lines = append(b_lines, *line)
		}
		changed = true
	}

	if changed {
		if *backup {
			if err := backup_local(*dest); err != nil {
				return err
			}
		}
		write_changes(b_lines)
	}

	return nil
}

func insert(slice []string, pos int, elements ...string) []string {
	return append(slice[:pos], append(elements, slice[pos:]...)...)
}
