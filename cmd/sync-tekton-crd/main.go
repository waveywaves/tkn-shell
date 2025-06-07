package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	tektonReleaseURL = "https://storage.googleapis.com/tekton-releases/pipeline/latest/release.yaml"
)

var (
	localCRDDir = "crd"
)

// CRD names to extract from the release.yaml
var crdNames = []string{
	"customruns.tekton.dev",
	"pipelines.tekton.dev",
	"pipelineruns.tekton.dev",
	"resolutionrequests.resolution.tekton.dev",
	"stepactions.tekton.dev",
	"tasks.tekton.dev",
	"taskruns.tekton.dev",
	"verificationpolicies.tekton.dev",
}

var rootCmd = &cobra.Command{
	Use:   "sync-tekton-crd",
	Short: "Sync Tekton CRD files from the main branch of tektoncd/pipeline repository",
	Long: `This command downloads the latest release.yaml from the tektoncd/pipeline 
repository and extracts individual CRD files to the local 'crd' directory.`,
	RunE: syncCRDs,
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func syncCRDs(cmd *cobra.Command, args []string) error {
	fmt.Println("🔄 Syncing Tekton CRDs from tektoncd/pipeline repository...")

	// Ensure the crd directory exists
	if err := os.MkdirAll(localCRDDir, 0755); err != nil {
		return fmt.Errorf("failed to create crd directory: %w", err)
	}

	// Download the release.yaml file
	fmt.Println("📥 Downloading latest release.yaml...")
	releaseContent, err := downloadReleaseYAML()
	if err != nil {
		return fmt.Errorf("failed to download release.yaml: %w", err)
	}

	// Parse and extract CRDs
	fmt.Println("🔍 Extracting CRDs from release.yaml...")
	if err := extractCRDs(releaseContent); err != nil {
		return fmt.Errorf("failed to extract CRDs: %w", err)
	}

	fmt.Println("🎉 CRD sync completed!")
	return nil
}

func downloadReleaseYAML() (string, error) {
	resp, err := http.Get(tektonReleaseURL)
	if err != nil {
		return "", fmt.Errorf("failed to download %s: %w", tektonReleaseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download %s: HTTP %d", tektonReleaseURL, resp.StatusCode)
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	return string(content), nil
}

func extractCRDs(releaseContent string) error {
	// Split the release.yaml into individual documents
	documents := strings.Split(releaseContent, "---")

	for _, doc := range documents {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}

		// Parse the document to check if it's a CRD
		var resource map[string]interface{}
		if err := yaml.Unmarshal([]byte(doc), &resource); err != nil {
			continue // Skip unparseable documents
		}

		// Check if this is a CustomResourceDefinition
		kind, ok := resource["kind"].(string)
		if !ok || kind != "CustomResourceDefinition" {
			continue
		}

		// Get the metadata
		metadata, ok := resource["metadata"].(map[string]interface{})
		if !ok {
			continue
		}

		// Get the CRD name
		name, ok := metadata["name"].(string)
		if !ok {
			continue
		}

		// Check if this is one of the CRDs we want to extract
		if !contains(crdNames, name) {
			continue
		}

		// Save the CRD to a file
		filename := fmt.Sprintf("%s.yaml", name)
		localPath := filepath.Join(localCRDDir, filename)

		if err := saveCRDToFile(localPath, doc); err != nil {
			fmt.Printf("❌ Failed to save %s: %v\n", filename, err)
			continue
		}

		fmt.Printf("✅ Extracted %s\n", filename)
	}

	return nil
}

func saveCRDToFile(path, content string) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", path, err)
	}
	defer file.Close()

	// Add document separator and content
	fullContent := fmt.Sprintf("---\n%s\n", content)

	_, err = file.WriteString(fullContent)
	if err != nil {
		return fmt.Errorf("failed to write content to %s: %w", path, err)
	}

	return nil
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func init() {
	rootCmd.Flags().StringVar(&localCRDDir, "output-dir", "crd", "Directory to save CRD files")
}
