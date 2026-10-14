//go:build !unix

package backend

import "os"

func lockFlatDisk(string) (*os.File, error) { return nil, os.ErrInvalid }
