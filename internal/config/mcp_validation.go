package config

import "fmt"

func (c MCPConfig) ValidateLimits() error {
	for index, server := range c.Servers {
		if server.MaxBytesPerCall < 0 {
			return fmt.Errorf("config: mcp.servers[%d].max_bytes_per_call cannot be negative", index)
		}
	}
	return nil
}
