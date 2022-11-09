/*
Copyright AppsCode Inc. and Contributors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cmds

import (
	"github.com/spf13/cobra"
	v "gomodules.xyz/x/version"
	cliflag "k8s.io/component-base/cli/flag"
)

func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:               "img-tools [command]",
		Short:             `OCI Image tools by AppsCode`,
		DisableAutoGenTag: true,
	}

	flags := rootCmd.PersistentFlags()
	// Normalize all flags that are coming from other packages or pre-configurations
	// a.k.a. change all "_" to "-". e.g. glog package
	flags.SetNormalizeFunc(cliflag.WordSepNormalizeFunc)

	rootCmd.AddCommand(NewCmdListImages())
	rootCmd.AddCommand(NewCmdCompletion())
	rootCmd.AddCommand(v.NewCmdVersion())

	return rootCmd
}
