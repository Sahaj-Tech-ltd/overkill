package main

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/ethos/internal/config"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage configuration",
}

var configShowCmd = &cobra.Command{
	Use:     "show",
	Short:   "Print current config with secrets masked",
	Aliases: []string{"cat"},
	RunE: func(cmd *cobra.Command, args []string) error {
		masked := cfg.MaskSecrets()
		data, err := toml.Marshal(masked)
		if err != nil {
			return fmt.Errorf("marshaling config: %w", err)
		}
		fmt.Print(string(data))
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a config value (dot notation)",
	Args:  cobra.ExactArgs(2),
	Example: "  ethos config set agent.name Butter\n" +
		"  ethos config set security.autonomy_level full\n" +
		"  ethos config set cost.daily_limit_usd 10.0",
	RunE: func(cmd *cobra.Command, args []string) error {
		key := args[0]
		value := args[1]

		parts := strings.Split(key, ".")
		if len(parts) < 2 {
			return fmt.Errorf("key must use dot notation (e.g. agent.name)")
		}

		field, err := resolveField(reflect.ValueOf(cfg), parts)
		if err != nil {
			return err
		}

		if err := setFieldValue(field, value); err != nil {
			return err
		}

		path := resolvedCfgPath
		if path == "" {
			p, err := config.ConfigPath()
			if err != nil {
				return err
			}
			path = p
		}

		if err := cfg.Save(path); err != nil {
			return err
		}

		fmt.Printf("%s✓ set %s = %s%s\n", colorGreen, key, value, colorReset)
		return nil
	},
}

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Get a config value",
	Args:  cobra.ExactArgs(1),
	Example: "  ethos config get agent.name\n" +
		"  ethos config get security.autonomy_level",
	RunE: func(cmd *cobra.Command, args []string) error {
		key := args[0]
		parts := strings.Split(key, ".")
		if len(parts) < 2 {
			return fmt.Errorf("key must use dot notation (e.g. agent.name)")
		}

		field, err := resolveField(reflect.ValueOf(cfg), parts)
		if err != nil {
			return err
		}

		fmt.Println(field.Interface())
		return nil
	},
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create default config file",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := resolvedCfgPath
		if path == "" {
			p, err := config.ConfigPath()
			if err != nil {
				return err
			}
			path = p
		}

		if err := cfg.Save(path); err != nil {
			return err
		}

		fmt.Printf("%sConfig initialized at %s%s\n", colorGreen, path, colorReset)
		return nil
	},
}

var configValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate current config",
	RunE: func(cmd *cobra.Command, args []string) error {
		errs := cfg.Validate()
		warns := cfg.Warnings()

		if len(errs) == 0 && len(warns) == 0 {
			fmt.Printf("%s✓ config is valid%s\n", colorGreen, colorReset)
			return nil
		}

		if len(errs) > 0 {
			fmt.Printf("%sErrors:%s\n", colorRed, colorReset)
			for _, e := range errs {
				fmt.Printf("  %s✗ %s%s\n", colorRed, e, colorReset)
			}
		}

		if len(warns) > 0 {
			fmt.Printf("%sWarnings:%s\n", colorYellow, colorReset)
			for _, w := range warns {
				fmt.Printf("  %s• %s%s\n", colorYellow, w, colorReset)
			}
		}

		if len(errs) > 0 {
			os.Exit(1)
		}

		return nil
	},
}

var configMigrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Force config migration and show changes",
	RunE: func(cmd *cobra.Command, args []string) error {
		migrated, changes, err := cfg.Migrate()
		if err != nil {
			return err
		}
		cfg = migrated

		if len(changes) == 0 {
			fmt.Printf("%sConfig is up to date%s\n", colorGreen, colorReset)
			return nil
		}

		fmt.Printf("%sApplied %d migration(s):%s\n", colorGreen, len(changes), colorReset)
		for _, c := range changes {
			fmt.Printf("  %s• %s%s\n", colorGreen, c, colorReset)
		}

		path := resolvedCfgPath
		if path == "" {
			p, err := config.ConfigPath()
			if err != nil {
				return err
			}
			path = p
		}

		return cfg.Save(path)
	},
}

func resolveField(v reflect.Value, parts []string) (reflect.Value, error) {
	for _, part := range parts {
		if v.Kind() == reflect.Ptr {
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct {
			return reflect.Value{}, fmt.Errorf("%q is not a struct", part)
		}

		found := false
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			tag := t.Field(i).Tag.Get("toml")
			if tag == part {
				v = v.Field(i)
				found = true
				break
			}
		}
		if !found {
			return reflect.Value{}, fmt.Errorf("unknown field %q", strings.Join(parts, "."))
		}
	}
	return v, nil
}

func setFieldValue(field reflect.Value, value string) error {
	if !field.CanSet() {
		return fmt.Errorf("field is not settable")
	}

	switch field.Kind() {
	case reflect.String:
		field.SetString(value)
	case reflect.Int:
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("expected integer, got %q", value)
		}
		field.SetInt(int64(n))
	case reflect.Float64:
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("expected float, got %q", value)
		}
		field.SetFloat(f)
	case reflect.Bool:
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("expected bool (true/false), got %q", value)
		}
		field.SetBool(b)
	default:
		return fmt.Errorf("unsupported type %s for field", field.Kind())
	}

	return nil
}

func init() {
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configInitCmd)
	configCmd.AddCommand(configValidateCmd)
	configCmd.AddCommand(configMigrateCmd)

	rootCmd.AddCommand(configCmd)
}
