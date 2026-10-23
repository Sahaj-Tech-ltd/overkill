package tools

import "os"

func openVisionFile(root *os.Root, name string) (*os.File, error) {
	return root.Open(name)
}
