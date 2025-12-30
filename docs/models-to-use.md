# Models to test eulix on

## 🔹 **Recommended 7 B Models for Code / Semantic Understanding**

### **1) CodeLlama-7B (Meta)**

* Specifically trained for **code completion, explanation, and reasoning** tasks.
* Great general-purpose coding model with multi-language support.
* Widely used as a strong baseline for code generation and comprehension. ([LinkedIn][1])

### **2) Qwen2.5-Coder-7B / Qwen3-Coder (Alibaba)**

* Designed specifically for **code generation, debugging, and reasoning**.
* Strong performance on benchmarks and supports **large context windows** (~32 K–128 K tokens) for whole projects. ([E2E Networks][2])

### **3) StarCoder2-7B (BigCode)**

* Open-source model trained on massive code datasets across languages.
* Performs well on **fill-in-the-middle completion** (good for AST-aware tasks) and general code reasoning. ([labellerr.com][3])

### **4) aiXcoder-7B**

* Research-backed code model with structured objectives that consider **syntax structures** and cross-file relationships, making it more aware of code semantics. ([arXiv][4])

### **5) Mistral 7B (General but strong)**

* Not code-specific out-of-the-box but still performs **competitively on coding tasks**, and fine-tuned versions can be effective for code reasoning. ([E2E Networks][2])

---

## 🔹 **Recommended 3 B Models for Code Tasks**

### **1) StarCoder2-3B**

* A lightweight variant of StarCoder2, supporting many languages and efficient code reasoning.
* Good for embedded or resource-constrained setups while still retaining semantic capability. ([labellerr.com][3])

### **2) Qwen2.5-Coder-3B**

* Smaller version of Qwen2.5-Coder with many of the same instruction-tuned capabilities (code completion, fixing, reasoning). ([E2E Networks][2])

### **3) Stable-Code-3B**

* Efficient 3 B code model with **fill-in-middle (FIM)** training, enabling better structural code completion than many generic 3 B models. ([Ollama][5])

---

## 📌 **What Each Type Does Best**

| Model              | Best For                                                |
| ------------------ | ------------------------------------------------------- |
| **CodeLlama-7B**   | Balanced coding, explanations, understanding algorithms |
| **Qwen-Coder-7B**  | Deep reasoning, large context understanding, debugging  |
| **StarCoder2-7B**  | Multilingual code base tasks & semantic completions     |
| **aiXcoder-7B**    | Syntax-aware code reasoning (AST-relevant)              |
| **Mistral 7B**     | Fast inference + general strong performance             |
| **StarCoder2-3B**  | Resource-efficient semantic coding                      |
| **Qwen-Coder-3B**  | Smaller device code reasoning                           |
| **Stable-Code-3B** | FIM and structure-aware code completion                 |

---

## 🧠 **Considerations for AST / Semantics**

* **Fine-tuning matters:** Models that are instruction-tuned on code tasks or trained with objectives that *explicitly consider code structure* (like fill-in-middle or syntax tasks) will understand semantic meaning beyond token sequences (closer to AST-like reasoning).
* **7 B variants generally outperform 3 B** for deeper semantic tasks (cross-file context, debugging explanations, logical reasoning).
* **Context window size** (e.g., 32 K+) helps models reason about larger codebases — especially important for semantic understanding across files. ([Baseten][6])

---

## 🛠️ **Practical Tips**

* For **local use with constraints**, choose **StarCoder2-3B** or **Stable-Code-3B**.
* For **serious development tasks requiring correctness and deep reasoning**, use **Qwen2.5-Coder-7B** or **CodeLlama-7B**.
* If you need **structured AST-aware completions**, consider models trained with FIM or custom objectives like **aiXcoder-7B**.

---

