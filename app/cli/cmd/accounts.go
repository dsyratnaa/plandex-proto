package cmd

import (
	"fmt"
	"plandex-cli/auth"

	"github.com/spf13/cobra"
)

func init() {
	RootCmd.AddCommand(accountsCmd)
	accountsCmd.AddCommand(clearAccountsCmd)
}

var accountsCmd = &cobra.Command{
	Use:   "accounts",
	Short: "Manage accounts",
}

var clearAccountsCmd = &cobra.Command{
	Use:   "clear",
	Short: "Clear all stored accounts",
	Run:   clearAccounts,
}

func clearAccounts(cmd *cobra.Command, args []string) {
	err := auth.ClearAccounts()
	if err != nil {
		fmt.Println("Error clearing accounts:", err)
		return
	}
	fmt.Println("Accounts cleared.")
}
