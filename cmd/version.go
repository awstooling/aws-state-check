package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
)

var (
	versionCommand = &cobra.Command{
		Use:   "version",
		Short: "",
		Run:   version}
)

func version(cmd *cobra.Command, args []string) {
	fmt.Println("0.0.2")
}

func init() {
	rootCmd.AddCommand(versionCommand)
}