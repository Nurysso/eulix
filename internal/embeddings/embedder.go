// internal/embeddings/embedder.go
// Install instructions:
// 1. Download: wget https://github.com/microsoft/onnxruntime/releases/download/v1.16.3/onnxruntime-linux-x64-1.16.3.tgz
// 2. Extract: tar -xzf onnxruntime-linux-x64-1.16.3.tgz
// 3. Install: sudo cp onnxruntime-linux-x64-1.16.3/lib/libonnxruntime.so* /usr/local/lib/ && sudo ldconfig
// Or set LD_LIBRARY_PATH to the lib directory

package embeddings

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/yalue/onnxruntime_go"
)

type Embedder struct {
	session      *onnxruntime_go.DynamicAdvancedSession
	inputShapes  []onnxruntime_go.Shape
	outputShapes []onnxruntime_go.Shape
	dimension    int
	modelPath    string
}

func NewEmbedder(modelName, backend string, dimension int) (*Embedder, error) {
	// Find model file based on model name
	modelPath, err := findModelPath(modelName)
	if err != nil {
		return nil, fmt.Errorf("model not found: %w", err)
	}

	// Set ONNX Runtime library path based on OS
	libPath := getONNXRuntimeLibPath()

	// Check if library exists
	if _, err := os.Stat(libPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("ONNX Runtime library not found at %s.\n\nInstall instructions:\n"+
			"1. Download: wget https://github.com/microsoft/onnxruntime/releases/download/v1.16.3/onnxruntime-linux-x64-1.16.3.tgz\n"+
			"2. Extract: tar -xzf onnxruntime-linux-x64-1.16.3.tgz\n"+
			"3. Install: sudo cp onnxruntime-linux-x64-1.16.3/lib/libonnxruntime.so* /usr/local/lib/ && sudo ldconfig\n"+
			"Or set LD_LIBRARY_PATH to the lib directory", libPath)
	}

	onnxruntime_go.SetSharedLibraryPath(libPath)

	if err := onnxruntime_go.InitializeEnvironment(); err != nil {
		return nil, fmt.Errorf("failed to initialize ONNX runtime: %w\n\nMake sure libonnxruntime.so is in your LD_LIBRARY_PATH", err)
	}

	// Create session options
	options, err := onnxruntime_go.NewSessionOptions()
	if err != nil {
		return nil, fmt.Errorf("failed to create session options: %w", err)
	}
	defer options.Destroy()

	// Set execution provider based on backend
	switch backend {
	case "cuda":
		// CUDA for NVIDIA GPUs
		cudaOptions, err := onnxruntime_go.NewCUDAProviderOptions()
		if err == nil {
			defer cudaOptions.Destroy()
			if err := options.AppendExecutionProviderCUDA(cudaOptions); err != nil {
				fmt.Println("CUDA not available, falling back to CPU")
			} else {
				fmt.Println("Using CUDA backend")
			}
		}
	case "cpu":
		// CPU is default
		fmt.Println("Using CPU backend")
	case "auto":
		// Try CUDA (for NVIDIA), then fall back to CPU
		// Note: ROCm support is not available in yalue/onnxruntime_go
		cudaOptions, cudaErr := onnxruntime_go.NewCUDAProviderOptions()
		if cudaErr == nil {
			defer cudaOptions.Destroy()
			if err := options.AppendExecutionProviderCUDA(cudaOptions); err == nil {
				fmt.Println("Auto-detected: Using CUDA backend")
			} else {
				fmt.Println("Auto-detected: Using CPU backend")
			}
		} else {
			fmt.Println("Auto-detected: Using CPU backend")
		}
	default:
		// Note: ROCm is not supported by yalue/onnxruntime_go bindings
		if backend == "rocm" {
			fmt.Println("ROCm backend not supported in this ONNX Runtime binding, using CPU")
		} else {
			fmt.Printf("Unknown backend '%s', using CPU\n", backend)
		}
	}

	// Create dynamic advanced session
	session, err := onnxruntime_go.NewDynamicAdvancedSession(
		modelPath,
		[]string{"input_ids", "attention_mask"},
		[]string{"last_hidden_state"},
		options,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create ONNX session: %w", err)
	}

	return &Embedder{
		session:   session,
		dimension: dimension,
		modelPath: modelPath,
	}, nil
}

func (e *Embedder) Embed(text string) ([]float32, error) {
	// Simple tokenization
	tokens := simpleTokenize(text)

	// Prepare input tensors
	inputIDs := make([]int64, len(tokens))
	attentionMask := make([]int64, len(tokens))
	for i := range tokens {
		inputIDs[i] = int64(tokens[i])
		attentionMask[i] = 1
	}

	// Create input shape [batch_size, sequence_length]
	inputShape := onnxruntime_go.NewShape(1, int64(len(tokens)))

	// Create input tensors
	inputIDsTensor, err := onnxruntime_go.NewTensor(inputShape, inputIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to create input_ids tensor: %w", err)
	}
	defer inputIDsTensor.Destroy()

	attentionMaskTensor, err := onnxruntime_go.NewTensor(inputShape, attentionMask)
	if err != nil {
		return nil, fmt.Errorf("failed to create attention_mask tensor: %w", err)
	}
	defer attentionMaskTensor.Destroy()

	// Run inference - DynamicAdvancedSession.Run signature:
	// Run(inputTensors []Value, outputTensors []Value) error
	// We need to pre-allocate output tensors
	outputTensors := make([]onnxruntime_go.Value, 1)

	err = e.session.Run(
		[]onnxruntime_go.Value{inputIDsTensor, attentionMaskTensor},
		outputTensors,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to run inference: %w", err)
	}

	if len(outputTensors) == 0 || outputTensors[0] == nil {
		return nil, fmt.Errorf("no output from model")
	}

	// Cleanup output after extraction
	defer func() {
		for _, output := range outputTensors {
			if output != nil {
				output.Destroy()
			}
		}
	}()

	// Extract embeddings from output
	// Convert Value to Tensor to access data
	outputTensor, ok := outputTensors[0].(*onnxruntime_go.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("unexpected output tensor type")
	}

	embeddings := outputTensor.GetData()

	// Mean pooling (simplified - assumes single batch)
	result := make([]float32, e.dimension)
	seqLen := len(embeddings) / e.dimension

	if seqLen == 0 {
		return nil, fmt.Errorf("invalid embedding dimensions")
	}

	for i := 0; i < e.dimension; i++ {
		sum := float32(0)
		for j := 0; j < seqLen; j++ {
			sum += embeddings[j*e.dimension+i]
		}
		result[i] = sum / float32(seqLen)
	}

	return result, nil
}

func (e *Embedder) Close() error {
	if e.session != nil {
		e.session.Destroy()
	}
	return nil
}

func getONNXRuntimeLibPath() string {
	switch runtime.GOOS {
	case "windows":
		// Windows default installation paths
		paths := []string{
			"C:\\Program Files\\onnxruntime\\lib\\onnxruntime.dll",
			"C:\\onnxruntime\\lib\\onnxruntime.dll",
			".\\onnxruntime.dll",
		}
		for _, path := range paths {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
		return "onnxruntime.dll" // Assume in PATH
	case "darwin":
		// macOS
		paths := []string{
			"/usr/local/lib/onnxruntime/lib/libonnxruntime.dylib",
			"/opt/homebrew/lib/libonnxruntime.dylib",
			"./libonnxruntime.dylib",
		}
		for _, path := range paths {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
		return "libonnxruntime.dylib"
	default:
		// Linux
		paths := []string{
			"/usr/local/lib/onnxruntime/lib/libonnxruntime.so",
			"/usr/lib/x86_64-linux-gnu/libonnxruntime.so",
			"/usr/lib/libonnxruntime.so",
			"./libonnxruntime.so",
		}
		for _, path := range paths {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
		return "libonnxruntime.so"
	}
}

func findModelPath(modelName string) (string, error) {
	// Platform-specific cache directories
	var cacheDir string
	switch runtime.GOOS {
	case "windows":
		cacheDir = filepath.Join(os.Getenv("LOCALAPPDATA"), "huggingface", "hub")
	case "darwin":
		cacheDir = filepath.Join(os.Getenv("HOME"), ".cache", "huggingface", "hub")
	default:
		cacheDir = filepath.Join(os.Getenv("HOME"), ".cache", "huggingface", "hub")
	}

	// Convert model name to Hugging Face cache format: "AUTHOR/MODEL" -> "models--AUTHOR--MODEL"
	hfModelDir := "models--" + strings.ReplaceAll(modelName, "/", "--")
	hfModelPath := filepath.Join(cacheDir, hfModelDir)

	// Try to find the model in HF cache structure
	if _, err := os.Stat(hfModelPath); err == nil {
		// Look for snapshots directory
		snapshotsPath := filepath.Join(hfModelPath, "snapshots")
		if entries, err := os.ReadDir(snapshotsPath); err == nil && len(entries) > 0 {
			// Use the first (or latest) snapshot
			snapshotPath := filepath.Join(snapshotsPath, entries[0].Name())

			// Check common ONNX model locations in the snapshot
			possibleFiles := []string{
				filepath.Join(snapshotPath, "model.onnx"),
				filepath.Join(snapshotPath, "onnx", "model.onnx"),
				filepath.Join(snapshotPath, "model_quantized.onnx"),
			}

			for _, modelFile := range possibleFiles {
				if _, err := os.Stat(modelFile); err == nil {
					return modelFile, nil
				}
			}
		}
	}

	// Fallback to other common locations
	locations := []string{
		filepath.Join(cacheDir, modelName, "model.onnx"),
		filepath.Join(cacheDir, modelName, "onnx", "model.onnx"),
		filepath.Join("models", modelName, "model.onnx"),
		filepath.Join("models", modelName, "onnx", "model.onnx"),
		filepath.Join(".", "models", modelName, "model.onnx"),
		filepath.Join(".", "model.onnx"),
		"model.onnx",
	}

	for _, path := range locations {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("model file not found for %s (HF dir: %s). Searched in: %v", modelName, hfModelPath, locations)
}

func simpleTokenize(text string) []int {
	tokens := []int{101} // [CLS] token

	for _, char := range text {
		if char >= 'a' && char <= 'z' {
			tokens = append(tokens, int(char)-'a'+1000)
		} else if char >= 'A' && char <= 'Z' {
			tokens = append(tokens, int(char)-'A'+2000)
		} else if char >= '0' && char <= '9' {
			tokens = append(tokens, int(char)-'0'+3000)
		} else if char == ' ' {
			tokens = append(tokens, 100) // Space token
		}
	}

	tokens = append(tokens, 102) // [SEP] token
	return tokens
}
