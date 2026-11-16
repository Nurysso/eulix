> **Note.** This is the formal write-up of PRISM v2, kept for readers who want the full maths. It was written against an earlier commit (the open issues in section 3.12 were reproduced at `039f796`), and the receiver-aware path for Python, TypeScript and Rust was added afterwards. For the current behaviour, start with [PRISM v2](../PRISM-V2.md).

# 3 Methodology

This section formalizes PRISMv2 (Polyglot Resolution via Inverted Symbol Map, version 2) as implemented in `libeulix/src/analyze/call_graph/prism_v2.rs` and its PHP submodule `prism_v2/php.rs` at revision `039f796`. Sections 3.2–3.4 define the input, the output and the indices; Sections 3.5–3.8 define the resolution procedure; Section 3.9 assembles the complete algorithm; Section 3.10 states its formal properties; Section 3.11 analyzes its cost; and Section 3.12 records where the behavior of the current implementation departs from the procedure as designed.

## 3.1 Scope and design objectives

PRISMv2 constructs an approximate whole-project call graph for repositories that mix programming languages. It runs after parsing. Language front-ends built on tree-sitter traverse the concrete syntax tree of each file and emit a per-file symbol summary of declarations, call sites, base-class references and imports. PRISMv2 consumes only these summaries and performs no syntax-tree traversal, type inference or data-flow analysis of its own. Its unit of work is the call site: for each call site it selects, among the project declarations whose index name equals the name written at the call site, the declaration most likely to be the target, or it abstains.

Three design objectives shape the procedure. Section 3.10 states the property that corresponds to each.

- **(O1) Precision before recall.** When the available evidence does not separate many candidates, the resolver emits no edge (Proposition 3).
- **(O2) Determinism.** The output depends on the input alone, and not on hash-map iteration order or on the thread schedule (Proposition 1).
- **(O3) Language-agnostic scoping.** One resolution procedure serves every supported language except PHP. It approximates lexical scope through four proxies: the file, explicit imports, the directory and the project. PHP is resolved by a dedicated rule set (Section 3.8.2), and PHP and non-PHP declarations never resolve to each other through name lookup (Proposition 2).

The procedure is neither sound nor complete with respect to the run-time call relation (Remark 2).

## 3.2 Problem definition

### 3.2.1 Input

A project is a finite set of source files $\mathcal{F}$ with $F = \lvert \mathcal{F} \rvert$. Each file $f$ has a unique path $p(f) \in \Sigma^{*}$ and a language family

$$\mathrm{fam}(f) \in \{\textsf{PHP},\ \textsf{GEN}\},$$

where $\textsf{PHP}$ denotes PHP and $\textsf{GEN}$ denotes every other supported language (Python, Go, Rust, TypeScript, JavaScript, Java, C and C++). Let $\mathrm{clean}(p)$ remove leading `./` from a path, and let $\mathrm{dir}(p)$ be the prefix of $\mathrm{clean}(p)$ before its last `/`, or $\varepsilon$ if there is none.

For every file the front-end supplies a summary $S(f) = (\mathrm{Fn}_f, \mathrm{Cl}_f, \mathrm{Imp}_f)$ with the following components.

- **Function declarations** $x \in \mathrm{Fn}_f$. Each has an identifier $\mathrm{id}(x)$, a declared name $\mathrm{nm}(x)$, an ordered parameter list $\pi(x) = (\pi_1, \pi_2, \dots)$, a tag set $T(x) \subseteq \{\textsf{nested}, \textsf{fallback}, \textsf{entry}\}$ and a sequence of call sites $\mathrm{Calls}(x)$. The tag $\textsf{nested}$ marks a function declared inside the body of another declaration, $\textsf{fallback}$ marks a PHP declaration guarded by `if (!function_exists(...))`, and $\textsf{entry}$ marks a program entry point.
- **Class declarations** $c \in \mathrm{Cl}_f$. Each has $\mathrm{id}(c)$, $\mathrm{nm}(c)$, a list of base-class strings $\mathrm{bases}(c)$, a source line $\mathrm{ln}(c)$ and a list of method declarations $\mathrm{Meth}(c)$ that are structured like function declarations.
- **Call sites** $\kappa = (\gamma, \mathrm{ln}, \mathrm{cnd}) \in \mathrm{Calls}(x)$. Here $\gamma \in \Sigma^{*}$ is the callee string as the front-end wrote it, $\mathrm{ln}$ is its source line, and $\mathrm{cnd} \in \{0, 1\}$ records whether the call lies inside a conditional, loop or `try` block.
- **Imports** $(m, A, t) \in \mathrm{Imp}_f$, consisting of a module string $m$, a list $A$ of imported item names and a type $t$. For PHP files, the types `include` and `include_dynamic` mark `include`/`require` directives with a literal and a computed path respectively.
- **PHP attributes.** PHP declarations carry their enclosing namespace $\mathrm{ns}(x)$ and, for methods, a flag $\mathrm{static}(x)$. PHP classes also carry the extended class $\mathrm{extends}(c)$ and the used traits $\mathrm{traits}(c)$. For PHP classes, $\mathrm{bases}(c)$ lists the extended class, the implemented interfaces and the used traits together.

**Identifiers.** The front-ends emit identifiers of the principal forms `func_n::p`, `method_C_n::p`, `class_n::p` and `struct_n::p`, where `p` is the path of the declaring file. A function `n` nested in a declaration whose identifier is `a::p` receives the identifier `a.n::p`. Since identifiers embed the declaring path, we state the following assumption.

> **(A1) File-unique identifiers.** No identifier is emitted by two different files.

PRISMv2 does not need (A1) in order to run, because duplicate identifiers are merged (Section 3.3.2). (A1) is used only to state Propositions 2 and 4 without exceptions.

### 3.2.2 Output

The output is a directed graph

$$G = (V,\ E), \qquad E = E_{\mathrm{call}} \uplus E_{\mathrm{inh}}.$$

Each node $v \in V$ carries a kind $k(v) \in \{\textsf{Fn}, \textsf{Me}, \textsf{Cl}\}$, its declaring file $\varphi(v)$, an entry-point flag, and the call in-degree

$$\mathrm{indeg}(v) = \bigl\lvert \{\, u : (u, v) \in E_{\mathrm{call}} \,\} \bigr\rvert,$$

which the implementation exports as `call_count_estimate`. Each edge $e$ carries its type (call or inheritance), a line $\mathrm{ln}(e)$ and a flag $\mathrm{cnd}(e)$.

### 3.2.3 Problem statement

Let $C^{\star} \subseteq V \times V$ be the run-time call relation: $(u, v) \in C^{\star}$ if some execution of $u$ invokes $v$. PRISMv2 does not compute $C^{\star}$. It computes the image of a deterministic partial resolution function $\mathrm{Res}$ over all call sites, and of a base-resolution function $\mathrm{RB}$ over all base references:

$$E_{\mathrm{call}} = \bigl\{\, \bigl(\mathrm{Id}(\mathrm{id}(x)),\ \mathrm{Res}(x, \kappa, f)\bigr) \;:\; f \in \mathcal{F},\ x \text{ declared in } f,\ \kappa \in \mathrm{Calls}(x),\ \mathrm{Res}(x, \kappa, f) \neq \bot \,\bigr\},$$

$$E_{\mathrm{inh}} = \bigl\{\, \bigl(\mathrm{Id}(\mathrm{id}(c)),\ \mathrm{RB}(\beta, f)\bigr) \;:\; c \in \mathrm{Cl}_f,\ \beta \in \mathrm{bases}(c),\ \mathrm{RB}(\beta, f) \notin \{\bot,\ \mathrm{Id}(\mathrm{id}(c))\} \,\bigr\}.$$

$\mathrm{Res}$ is Algorithm 4 for $\textsf{GEN}$ files and Algorithm 5 for $\textsf{PHP}$ files; $\mathrm{RB}$ is $\textsc{Resolve-Base}$ (Algorithm 4) and $\textsc{PhpClass}$ (Algorithm 5) respectively. For each retained pair, $\mathrm{ln}$ is the minimum line and $\mathrm{cnd}$ the conjunction of the flags over the witnessing call sites (Section 3.9). In general neither $E_{\mathrm{call}} \subseteq C^{\star}$ nor $C^{\star} \subseteq E_{\mathrm{call}}$ holds, so the quality of the approximation is an empirical question.

## 3.3 Preliminaries

### 3.3.1 Canonical file order

Files are sorted lexicographically by the key $(\mathrm{dir}(p(f)),\ p(f))$. Because paths are unique, the order is total. We write $f_0, \dots, f_{F-1}$ for the sorted files and $i(f)$ for the position of $f$. The directory identifier is

$$d(f_0) = 0, \qquad d(f_i) = d(f_{i-1}) + \bigl[\, \mathrm{dir}(p(f_i)) \neq \mathrm{dir}(p(f_{i-1})) \,\bigr].$$

Since files with equal directories are adjacent in the order, $d$ is non-decreasing in $i$ and maps distinct directories to distinct identifiers.

### 3.3.2 Nodes

Declarations are emitted file by file in canonical order. Within a file, the function declarations come first in source order, followed, for each class in source order, by the class and then its methods. The node sequence $V$ is the subsequence of first occurrences of each identifier ("first wins"). We write $\iota(v)$ for the position of $v$ in $V$ and $\mathrm{Id} : \Sigma^{*} \rightharpoonup V$ for the map from an identifier to its node. Kinds are assigned as

$$k(x) = \begin{cases} \textsf{Cl} & x \text{ is a class}, \\ \textsf{Me} & x \in \mathrm{Meth}(c) \text{ for some } c, \text{ or } \mathrm{id}(x) \text{ begins with \texttt{method\_} and } \textsf{nested} \notin T(x), \\ \textsf{Fn} & \text{otherwise.} \end{cases}$$

A node $v$ that originates from $\mathrm{Meth}(c)$ has the owner $\mathrm{owner}(v) = \mathrm{Id}(\mathrm{id}(c))$. We write $\mathrm{fb}(v) = 1$ if $v$ carries the tag $\textsf{fallback}$ and $\mathrm{fb}(v) = 0$ otherwise, and $\mathrm{nested}(v)$ analogously.

**Observation 1.** $\iota$ is non-decreasing in $i(\varphi(\cdot))$: if $\iota(v) < \iota(v')$ then $i(\varphi(v)) \le i(\varphi(v'))$. Emission proceeds file by file in canonical order, and first-wins deduplication preserves relative order.

### 3.3.3 Index names

Every node is indexed under a name $\eta(v)$. For $\textsf{PHP}$ nodes,

$$\eta(v) = \begin{cases} \mathrm{lower}\bigl(\mathrm{ns}(v)\ \texttt{\textbackslash}\ \mathrm{nm}(v)\bigr) & v \text{ a function or class with a namespace}, \\ \mathrm{lower}\bigl(\mathrm{nm}(v)\bigr) & \text{otherwise (including every method)}, \end{cases}$$

which reflects the case-insensitivity of PHP names. For $\textsf{GEN}$ nodes, $\eta(v) = \mathrm{bare}(\mathrm{id}(v))$, where

$$\mathrm{bare}(a) = \mathrm{last}_{\texttt{.}}\Bigl(\mathrm{last}_{\texttt{::}}\bigl(\mathrm{strip}_{P}(h(a))\bigr)\Bigr).$$

Here $h(a)$ removes the last `::`-separated segment of $a$ (the path suffix), $\mathrm{strip}_P$ removes the first matching prefix in the list $P = (\texttt{func\_}, \texttt{method\_}, \texttt{class\_}, \texttt{struct\_})$, and $\mathrm{last}_{\delta}$ returns the part after the last occurrence of the separator $\delta$, or the whole string if $\delta$ does not occur. For example, `func_newFileCache::cli/x.go` and `func_outer.helper::a.rs` are indexed as `newFileCache` and `helper`. Section 3.12 (F1) discusses the value of $\mathrm{bare}$ on method identifiers.

### 3.3.4 Callee decomposition

$\textsc{Split}(\gamma) = (q, s)$ cuts $\gamma$ at the right-most occurrence of either separator `.` or `::`. The qualifier $q$ is the part before it and the name $s$ the part after it; if neither separator occurs, $(q, s) = (\varepsilon, \gamma)$. The functions $\textsc{Root}(q)$ and $\textsc{Last}(q)$ return the first and the last segment of $q$ under the separator set $\{\texttt{.}, \texttt{:}, \texttt{/}\}$. The implicit-receiver spellings are

$$\mathrm{SELF} = \{\texttt{self}, \texttt{this}, \texttt{cls}, \texttt{\_self}, \texttt{self\_}, \texttt{myself}\},$$

and a qualifier $q$ is a self-qualifier if $q \in \mathrm{SELF} \cup \{\texttt{Self}\}$.

### 3.3.5 Directory proximity

For paths $a$ and $b$ (after $\mathrm{clean}$), let $\lambda(a, b)$ be the byte length of the longest common prefix of $a$ and $b$ that ends in `/`, or $0$ if there is none. For instance, $\lambda(\texttt{src/a/x.py}, \texttt{src/a/y.py}) = 6$ and $\lambda(\texttt{src/ab/x.py}, \texttt{src/abc/y.py}) = 4$.

**Lemma 1 (proximity).** For a fixed $a$, $\lambda(a, b) > \lambda(a, b')$ if and only if the deepest directory that contains both $a$ and $b$ lies strictly below the deepest directory that contains both $a$ and $b'$.

*Proof.* The prefixes of $a$ that end in `/` are exactly the ancestor directories of $a$, and they are totally ordered by length. $\lambda(a, b)$ is the length of the longest of them that is also a prefix of $b$, which is the deepest common ancestor directory of $a$ and $b$. $\square$

Maximizing $\lambda$ over a candidate set is therefore a nearest-neighbor search in the directory tree. Equivalently, it minimizes the distance $\mathrm{dist}(a, b) = H - \mathrm{depth}(\mathrm{lca}(a, b))$ for a fixed maximum depth $H$, which is an ultrametric.

## 3.4 Indices

### 3.4.1 The inverted symbol map

For every index name $s$,

$$B(s) = \bigl\langle\, v \in V \;:\; \eta(v) = s \ \wedge\ \neg\,\mathrm{nested}(v) \,\bigr\rangle \quad \text{ordered by } \iota.$$

Nested functions are excluded, so they are reachable only through the exact-identifier rule of Section 3.8.1.

**Lemma 2 (contiguity).** For every $s$, every file $f$ and every directory identifier $\delta$, the subsequences

$$B(s)\big|_{f} = \langle v \in B(s) : \varphi(v) = f \rangle \qquad \text{and} \qquad B(s)\big|_{\delta} = \langle v \in B(s) : d(\varphi(v)) = \delta \rangle$$

are contiguous intervals of $B(s)$, and each is located by two binary searches in $O(\log \lvert B(s) \rvert)$ time.

*Proof.* By Observation 1, $v \mapsto i(\varphi(v))$ is non-decreasing along $B(s)$, and $d$ is non-decreasing in $i$, so $v \mapsto d(\varphi(v))$ is non-decreasing as well. The preimage of a single value under a non-decreasing map on a sequence is an interval, and its two endpoints are partition points. $\square$

Only exact directories form intervals. A directory subtree generally does not, because under byte order `a` $<$ `a-c` $<$ `a/b`. This is why the scope ladder of Section 3.6 has no subtree tier.

### 3.4.2 The method index

$$\mathcal{M} = \mathrm{sort}\bigl\{\, (\iota(\mathrm{owner}(v)),\ \eta(v),\ \iota(v)) \;:\; v \in V,\ \mathrm{owner}(v) \text{ defined} \,\bigr\}$$

in lexicographic order, and

$$\mathrm{own}(c, s) = \operatorname*{arg\,min}_{v} \bigl\{\, \iota(v) : (\iota(c), s, \iota(v)) \in \mathcal{M} \,\bigr\},$$

or $\bot$ if the set is empty. One binary search in $\mathcal{M}$ computes $\mathrm{own}(c, s)$.

### 3.4.3 Module index and import environments ($\textsf{GEN}$)

Let $\mathrm{stem}(p)$ be $\mathrm{clean}(p)$ without its extension, where the extension starts at the last `.` and only counts as one if no `/` follows that `.`. For $t = t_1/\cdots/t_k$ let $\mathrm{suf}(t) = \{\, t_i/\cdots/t_k : 1 \le i \le k \,\}$. The module index maps every key to a sorted list of files:

$$\mathcal{I}(\mathit{key}) = \bigl\{\, f \in \mathcal{F} \;:\; \mathit{key} \in \mathrm{suf}(\mathrm{stem}(p(f))) \cup \mathrm{suf}(\mathrm{dir}(\mathrm{stem}(p(f)))) \,\bigr\}.$$

Any module syntax that can be written as a path suffix (a Python or Rust module, a JavaScript specifier, a Go package directory) can therefore be looked up without language-specific rules.

A module string $m$ is normalized as follows. Surrounding blanks and leading `.` and `/` characters are removed; the prefixes `@/`, `~/`, `crate::`, `self::` and `super::` are removed, each prefix with all of its leading repetitions and in that order; and if the result contains no `/` but contains `.` or `::`, both separators are replaced by `/`. If the normalized string $n = n_1/\cdots/n_j$ is empty, $\mathrm{res}(m) = \textsf{ext}$. Otherwise

$$\mathrm{res}(m) = \begin{cases} \mathcal{I}(n_{i^{*}}/\cdots/n_j) & \text{if } i^{*} = \min\{\, i : n_i/\cdots/n_j \in \mathrm{dom}(\mathcal{I}),\ j - i + 1 \ge \min(j, 2) \,\} \text{ exists}, \\ \textsf{ext} & \text{otherwise,} \end{cases}$$

so the longest suffix that is a key wins, and a suffix of fewer than $\min(j, 2)$ components is never used. The value $\textsf{ext}$ means that the module lies outside the project (a standard or third-party library).

For a $\textsf{GEN}$ file $f$, two partial maps $N_f, Q_f : \Sigma^{*} \rightharpoonup 2^{\mathcal{F}} \cup \{\textsf{ext}\}$ form its import environment. $N_f$ covers names that may be called bare (`foo()`), and $Q_f$ covers names that may head a qualified call (`ns.foo()`). Imports are processed in order, and $X[a] \leftarrow_{1} r$ assigns $r$ only if $a \notin \mathrm{dom}(X)$, so the first registration wins. For each import $(m, A, t)$ with $r = \mathrm{res}(m)$:

1. for each $a \in A$: $N_f[a] \leftarrow_{1} r$ and $Q_f[a] \leftarrow_{1} r$;
2. with $m'$ equal to $m$ without a trailing `/`: $Q_f[\text{last non-empty segment of } m'] \leftarrow_{1} r$;
3. if $m'$ contains no `/`: $Q_f[\text{first non-empty segment of } m'] \leftarrow_{1} r$.

Segments are taken under the separator set $\{\texttt{.}, \texttt{:}, \texttt{/}\}$. For $\textsf{PHP}$ files both maps are empty, because the PHP front-end already applies `use` imports when it writes fully qualified class names.

### 3.4.4 Include closure ($\textsf{PHP}$)

An include directive with expression $m$ in file $f$ resolves to at most one file. Let $m'$ be $m$ with `\` replaced by `/`, and let $\mathrm{norm}$ collapse `.` and `..` segments ($\mathrm{norm}$ is undefined if the path climbs above the root).

1. **Path match (literal includes only).** Try $\mathrm{norm}(\mathrm{dir}(p(f))/m')$, or $\mathrm{norm}(m')$ when $\mathrm{dir}(p(f)) = \varepsilon$ or $m'$ begins with `/`, and then $\mathrm{norm}(m')$. Return the first that is the path of a project file.
2. **Suffix match.** Let the tail $t$ be $m'$ for a literal include. For a computed include, $t$ is the maximal suffix of path characters (alphanumerics, `_`, `-`, `.`, `/`) that ends at the last case-insensitive occurrence of `.php`, `.inc` or `.phtml`, with a leading interpolated variable name removed. $t$ is undefined if its file name begins with `.`. Discard everything up to the last `../` in $t$. Among the files whose base name equals the last segment of $t$, score each file $g$ by
$$\mathrm{score}(g) = \bigl(\, \text{number of trailing path segments shared by } p(g) \text{ and } t,\ \ \lambda(p(f), p(g)) \,\bigr)$$
in lexicographic order, and return the unique maximizer. If the maximum is attained more than once, return $\bot$.

$\mathrm{Inc}(f)$ is the sorted, deduplicated set of resolved targets of the directives of $f$. The closure $\mathrm{Inc}^{*}(f)$ consists of the first $\min(X, \lvert R(f) \rvert)$ files of a breadth-first enumeration of $R(f)$, the set of files reachable from $f$ through one or more include steps (excluding $f$ itself), with $X = 2048$. It is stored sorted. The cap $X$ bounds the closure of a bootstrap file that includes most of the project.

## 3.5 Candidate selection

A lookup states which kinds of target it accepts through a *seek mode* $\sigma$. A shape mode $\mathrm{Shape}(w)$, with call shape $w \in \{\textsf{Call}, \textsf{MethodCall}, \textsf{Class}\}$, ranks the kinds that a call of that syntactic shape may reach. An exact mode $\mathrm{Only}(k^{*})$ accepts exactly one kind and is used by the PHP rules, where the call syntax determines the target kind. The rank $\rho(\sigma, k, \chi)$, where lower is better and $\chi \in \{0, 1\}$ is a strictness flag, is defined by Table 1 and by $\rho(\mathrm{Only}(k^{*}), k, \chi) = 0$ if $k = k^{*}$ and $\bot$ otherwise.

**Table 1.** Rank $\rho(\mathrm{Shape}(w), k, \chi)$; $\bot$ marks an inadmissible kind.

| Call shape $w$ | $k = \textsf{Fn}$ | $k = \textsf{Cl}$ | $k = \textsf{Me}$ |
| --- | :---: | :---: | :---: |
| $\textsf{Call}$ (`foo()`) | 0 | 1 | 2 if $\chi = 0$; $\bot$ if $\chi = 1$ |
| $\textsf{MethodCall}$ (`x.foo()`, receiver of unknown class) | 1 | 2 | 0 |
| $\textsf{Class}$ (base class, class qualifier) | $\bot$ | 0 | $\bot$ |

The off-diagonal entries admit call forms whose syntax under-determines the kind of the target. A bare call may instantiate a class or invoke a method through an implicit receiver, and a qualified call on an unknown receiver may denote a module-level function. The strict flag, which is set at project scope, withdraws one of these readings: a bare call to a method.

Given a candidate sequence $C$, a seek mode $\sigma$, strictness $\chi$, the caller's file $f$ and a proximity switch $\omega \in \{0, 1\}$, the eligible set is

$$\mathrm{Elig}(C) = \bigl\{\, v \in C \;:\; \mathrm{fam}(\varphi(v)) = \mathrm{fam}(f) \ \wedge\ \rho(\sigma, k(v), \chi) \neq \bot \,\bigr\},$$

each eligible candidate receives the key

$$\mathrm{key}(v) = \Bigl(\ \rho(\sigma, k(v), \chi),\ \ \mathrm{fb}(v),\ \ -\,\omega \cdot \lambda\bigl(p(f), p(\varphi(v))\bigr),\ \ \iota(v)\ \Bigr),$$

ordered lexicographically, and

$$\textsc{Pick}(C, \sigma, \chi, f, \omega) = \Bigl(\ \operatorname*{arg\,min}_{v \in \mathrm{Elig}(C)} \mathrm{key}(v),\ \ \bigl\lvert \mathrm{Elig}(C) \bigr\rvert\ \Bigr),$$

with first component $\bot$ if $\mathrm{Elig}(C) = \emptyset$. The key encodes a strict priority: fit between the call shape and the target kind, then genuine definitions before PHP fallbacks, then directory proximity, then declaration order. The minimizer is unique because $\iota$ is injective. The count $\lvert \mathrm{Elig}(C) \rvert$ is the ambiguity measure that the abstention rule of Section 3.6 uses.

```text
Algorithm 1  PICK(C, σ, χ, f, ω)
Input : candidate sequence C; seek mode σ; strictness χ ∈ {0,1};
        caller file f; proximity switch ω ∈ {0,1}
Output: (selected node or ⊥, number of eligible candidates)
 1: best ← ⊥;  bestkey ← +∞;  n ← 0
 2: for each v ∈ C do
 3:     if fam(φ(v)) ≠ fam(f) then continue              ▷ PHP and GEN never mix
 4:     r ← ρ(σ, k(v), χ);  if r = ⊥ then continue        ▷ kind inadmissible for σ
 5:     n ← n + 1
 6:     key ← (r, fb(v), −ω · λ(p(f), p(φ(v))), ι(v))     ▷ lexicographic
 7:     if key < bestkey then (best, bestkey) ← (v, key)
 8: return (best, n)
```

**Lemma 3 (proximity is constant within a file or a directory).** If every $v \in C$ satisfies $\varphi(v) = f$, or every $v \in C$ satisfies $d(\varphi(v)) = d(f)$, then $\textsc{Pick}(C, \sigma, \chi, f, 0)$ and $\textsc{Pick}(C, \sigma, \chi, f, 1)$ select the same node.

*Proof.* Every path in a directory $D$ has the form $D/n$ with $n$ free of `/`. For two such paths the longest common prefix that ends in `/` is $D/$, so $\lambda = \lvert D \rvert + 1$, or $\lambda = 0$ if $D = \varepsilon$. The third key component is therefore constant over $C$ and cannot change the minimizer. $\square$

The implementation uses this lemma to skip the $O(L)$ proximity computation in the same-file and same-directory tiers.

## 3.6 The scope ladder

$\textsc{Lookup}(s, f, \sigma)$ resolves the name $s$ for a caller in file $f$ by searching scopes from nearest to farthest. It is parameterized by the ambiguity threshold $\tau = \max(\tau_0, 1)$, where $\tau_0$ is the option `max_ambiguous` (default 8), and by the strict-import flag $\varsigma$, the option `strict_imports` (default true). We also write

$$\textsc{LookupIn}(s, \Phi, f, \sigma) = \text{first component of } \textsc{Pick}\bigl(\langle v \in B(s) : \varphi(v) \in \Phi \rangle,\ \sigma,\ 0,\ f,\ 1\bigr)$$

for a lookup restricted to a sorted file set $\Phi$. Membership $\varphi(v) \in \Phi$ is tested by binary search.

**Table 2.** Tiers of $\textsc{Lookup}$. Each tier draws its candidates from $B(s)$.

| Tier | Candidates $v \in B(s)$ with | $\chi$ | $\omega$ | Rule |
| --- | --- | :---: | :---: | --- |
| T1 same file | $\varphi(v) = f$ | 0 | 0 | first hit wins |
| T2 explicit import | $\varphi(v) \in N_f(s)$ | 0 | 1 | veto: if $N_f(s) = \textsf{ext}$ and $\varsigma$, return $\bot$ |
| T2b PHP includes | $\varphi(v) \in \mathrm{Inc}^{*}(f)$ | 0 | 1 | first hit wins; empty for $\textsf{GEN}$ |
| T3 same directory | $d(\varphi(v)) = d(f)$ | 0 | 0 | hold: a fallback winner $h$ is deferred |
| T4 project | $\mathrm{fb}(v) = 0$ | 1 | 1 | cap: return the winner if $n \le \tau$, otherwise $h$ |
| T5 project fallbacks | $\mathrm{fb}(v) = 1$ | 1 | 1 | runs only if T4 has no eligible candidate and $h = \bot$; cap $\tau$ |

```text
Algorithm 2  LOOKUP(s, f, σ)                     ▷ scope ladder; returns a node or ⊥
Require: B, N_f, Inc*(f), d, τ = max(τ0, 1), ς
 1: if B(s) is undefined then return ⊥
 2: C ← B(s)
 3: (x, _) ← PICK(C|file = f, σ, 0, f, 0)                        ▷ T1, interval by Lemma 2
 4: if x ≠ ⊥ then return x
 5: if s ∈ dom(N_f) then                                          ▷ T2
 6:     if N_f(s) = Φ ⊆ 𝓕 then
 7:         x ← LOOKUPIN(s, Φ, f, σ);  if x ≠ ⊥ then return x
 8:     else if ς then return ⊥                                   ▷ veto: external import
 9: if Inc*(f) ≠ ∅ then                                           ▷ T2b
10:     x ← LOOKUPIN(s, Inc*(f), f, σ);  if x ≠ ⊥ then return x
11: h ← ⊥
12: (x, _) ← PICK(C|dir = d(f), σ, 0, f, 0)                      ▷ T3, interval by Lemma 2
13: if x ≠ ⊥ then
14:     if fb(x) = 0 then return x
15:     h ← x                                                     ▷ hold a PHP fallback
16: (x, n) ← PICK(⟨v ∈ C : fb(v) = 0⟩, σ, 1, f, 1)               ▷ T4
17: if x ≠ ⊥ then
18:     if n ≤ τ then return x else return h                     ▷ abstention cap
19: if h ≠ ⊥ then return h
20: (x, n) ← PICK(⟨v ∈ C : fb(v) = 1⟩, σ, 1, f, 1)               ▷ T5
21: if x ≠ ⊥ and n ≤ τ then return x else return ⊥
```

Three rules depart from a plain "first tier with a result wins" order. The *veto* (line 8) treats an import from outside the project as decisive evidence that no project declaration is meant. The *hold* (line 15) lets a genuine definition anywhere in the project override a same-directory PHP stub declared under `function_exists`. The *cap* (line 18) abstains when, at project scope, more than $\tau$ declarations remain eligible. In that case the held fallback, if any, is returned. The explicit-import and include tiers precede the same-directory tier because an import is direct evidence of a dependency, which a sibling file is not.

## 3.7 Class-hierarchy resolution

Let $\mathrm{Par}(c)$ and $\mathrm{Chi}(c)$ list the targets and the sources of the inheritance edges at $c$, so that $\mathrm{Par}(c) = \langle v : (c, v) \in E_{\mathrm{inh}} \rangle$ and $\mathrm{Chi}(c) = \langle u : (u, c) \in E_{\mathrm{inh}} \rangle$. Both are ordered by $\iota$, because $E_{\mathrm{inh}}$ is sorted by $(\mathrm{from}, \mathrm{to})$ (Section 3.9). Let $\mathrm{first}(a_1, a_2, \dots)$ denote the first argument that is not $\bot$, evaluated lazily from left to right, and let $K = 64$.

```text
Algorithm 3  WALK(c, s, A) and MRO(c, s)
 1: function WALK(c, s, A)                          ▷ A ∈ {Par, Chi}
 2:     Q ← FIFO queue containing A(c) in order;  Vis ← ⟨c⟩
 3:     while Q ≠ ∅ do
 4:         c' ← DEQUEUE(Q)
 5:         if c' ∈ Vis then continue                ▷ cycle guard
 6:         append c' to Vis
 7:         if |Vis| > K then return ⊥               ▷ budget: at most K classes incl. c
 8:         if own(c', s) ≠ ⊥ then return own(c', s)
 9:         ENQUEUE every element of A(c') in order
10:     return ⊥
11: function MRO(c, s)
12:     return FIRST(own(c, s), WALK(c, s, Par), WALK(c, s, Chi))
```

$\textsc{Mro}$ searches the class itself, then its ancestors, then its descendants. Ancestors come first because inherited methods are the common case. Descendants are searched last so that an override can be found when the call is written against a base class. Each $\textsc{Walk}$ returns a defining class at minimum hop distance among the classes it visits. Ties at distance one are broken by $\iota$, and ties further out by breadth-first discovery order.

## 3.8 Call-site resolution

### 3.8.1 Non-PHP code ($\textsf{GEN}$)

Each call site is resolved in the context of its enclosing declaration $x$ in file $f$. The context consists of the caller node $u = \mathrm{Id}(\mathrm{id}(x))$; the enclosing class $c_u = \mathrm{Id}(\mathrm{id}(c))$ if $x \in \mathrm{Meth}(c)$ and $c_u = \bot$ for free functions; and the receiver flag $r_u = [\, \pi_1(x) \in \mathrm{SELF} \,]$ for methods, with $r_u = 0$ for free functions.

**Parameter shadowing.** Let $\mathrm{pn}(\pi_i)$ remove the leading markers `mut `, `&`, `*` and `...` (in this order) and surrounding blanks from a parameter name. Then

$$\mathrm{shadow}(x, \gamma) \iff \textsc{Split}(\gamma) = (\varepsilon, \gamma)\ \wedge\ \exists\, \pi_i \in \pi(x) : \mathrm{pn}(\pi_i) = \gamma \ \wedge\ \mathrm{pn}(\pi_i) \notin \mathrm{SELF}.$$

A bare call through a parameter (for example a callback named `helper`) therefore never resolves to a global declaration that shares the parameter's name.

**Nested functions.** Split $\mathrm{id}(x)$ at its first `::` into $a$ and $b$. Then $\textsc{Nest}(x, \gamma) = \mathrm{Id}(a\,\texttt{.}\,\gamma\,\texttt{::}\,b)$ if that node exists and is nested, and $\bot$ otherwise. Because nested functions are absent from $B$ (Section 3.4.1), a nested function is visible only to its immediately enclosing declaration.

```text
Algorithm 4  RESOLVE-GEN(x, γ, f, c_u, r_u) and RESOLVE-BASE(β, f)
 1: function RESOLVE-GEN(x, γ, f, c_u, r_u)
 2:     if shadow(x, γ) then return ⊥                       ▷ parameter hides globals
 3:     v ← NEST(x, γ);  if v ≠ ⊥ then return v               ▷ lexically nested function
 4:     if γ ∈ dom(Id) then return Id(γ)                      ▷ exact identifier
 5:     if γ = ε or γ contains "/" then return ⊥
 6:     (q, s) ← SPLIT(γ);  if s = ε then return ⊥
 7:     if q = ε then                                         ▷ bare call  s()
 8:         if r_u = 1 and c_u ≠ ⊥ then
 9:             v ← MRO(c_u, s);  if v ≠ ⊥ then return v      ▷ implicit receiver
10:         return LOOKUP(s, f, Shape(Call))
11:     if q ∈ SELF ∪ {Self} then                             ▷ self.s(), this.s(), Self::s()
12:         return FIRST(MRO(c_u, s) if c_u ≠ ⊥ else ⊥, LOOKUP(s, f, Shape(MethodCall)))
13:     a ← ROOT(q)                                           ▷ ns.s(), pkg.s()
14:     if a ∈ dom(Q_f) then
15:         if Q_f(a) = Φ ⊆ 𝓕 then
16:             v ← LOOKUPIN(s, Φ, f, Shape(Call));  if v ≠ ⊥ then return v
17:         else if ς then return ⊥                           ▷ external module
18:     k ← LOOKUP(LAST(q), f, Shape(Class))                  ▷ Cls.s(), Cls::s()
19:     if k ≠ ⊥ then
20:         v ← MRO(k, s);  if v ≠ ⊥ then return v
21:     return LOOKUP(s, f, Shape(MethodCall))
22: function RESOLVE-BASE(β, f)
23:     if β ∈ dom(Id) then return Id(β)
24:     β' ← prefix of β before the first of "<", "[", "(", trimmed   ▷ Base<T> → Base
25:     (q, s) ← SPLIT(β')
26:     if q ≠ ε and Q_f(ROOT(q)) = Φ ⊆ 𝓕 then
27:         v ← LOOKUPIN(s, Φ, f, Shape(Class));  if v ≠ ⊥ then return v
28:     return LOOKUP(s, f, Shape(Class))
```

In the implementation, lines 2–3 are evaluated by the edge-construction loop (Algorithm 6) before $\textsc{Resolve-Gen}$ proper is called. Base references are not subject to the external-import veto.

### 3.8.2 PHP code ($\textsf{PHP}$)

The PHP front-end writes class names fully qualified, with `use` aliases and the current namespace applied. It writes member calls `$x->m()` as `x.m` and substitutes the receiver's class (`\App\User.m`) where it can infer that class. PRISMv2 relies on these conventions and adds the following structures. For each PHP class $c$, $\mathrm{ext}(c)$ is the resolved class it extends (the first `extends` entry), $\mathrm{Tr}(c)$ is the list of resolved traits it uses, and $\mathrm{IsTrait}$ is the set of classes used as a trait anywhere. All three are resolved through $\textsc{PhpClass}$. The caller context consists of $c_u$ as in Section 3.8.1 and the namespace $\mathrm{ns}_u$ of the enclosing declaration. The parameter-shadowing and nested-function steps do not apply.

```text
Algorithm 5  RESOLVE-PHP(γ, f, c_u, ns_u) and its helpers
 1: function PHPCLASS(β, f)
 2:     return LOOKUP(lower(β trimmed, leading "\" removed), f, Only(Cl))
 3: function TRAITS(c, t)                 ▷ BFS over Tr from Tr(c), at most K classes
 4:     return own(t', t) for the first trait t' reached with own(t', t) ≠ ⊥, else ⊥
 5: function UP(c, t)                     ▷ PHP lookup order
 6:     c' ← c
 7:     repeat at most K times while c' ≠ ⊥
 8:         v ← FIRST(own(c', t), TRAITS(c', t));  if v ≠ ⊥ then return v
 9:         c' ← ext(c')
10:     return WALK(c, t, Par)            ▷ all bases; interfaces only declare, so last
11: function FIND(c, t, disp)
12:     return FIRST(UP(c, t), WALK(c, t, Chi) if disp else ⊥)
13: function METHOD(c, t, st, disp)       ▷ with magic-method fallback
14:     return FIRST(FIND(c, t, disp), FIND(c, "__callstatic" if st else "__call", disp))
15: function BYNAME(t, f, st)             ▷ receiver of unknown class
16:     (v, n) ← PICK(B(t), Only(Me), 1, f, 0)
17:     if v ≠ ⊥ and n = 1 and (st or static(v) = 0) then return v else return ⊥
18: function RESOLVE-PHP(γ, f, c_u, ns_u)
19:     if γ = "new " β then return PHPCLASS(β, f)
20:     if γ = "parent::" t then
21:         if c_u ≠ ⊥ and ext(c_u) ≠ ⊥ then return UP(ext(c_u), lower(t)) else return ⊥
22:     (q, s) ← SPLIT(γ)
23:     if s begins with "$" or "{" then return ⊥             ▷ name known only at run time
24:     s ← lower(s)
25:     if q = ε then                                         ▷ function call
26:         if s = "\" s' then return LOOKUP(s', f, Only(Fn)) ▷ fully qualified
27:         if s contains "\" then (loc, glob) ← (s, last "\"-segment of s)
28:         else (loc, glob) ← (lower(ns_u) "\" s if ns_u ≠ ⊥ else ⊥, s)
29:         return FIRST(LOOKUP(loc, f, Only(Fn)) if loc ≠ ⊥ else ⊥, LOOKUP(glob, f, Only(Fn)))
30:     st ← [γ continues with "::" after q]                  ▷ static call syntax
31:     disp ← (st = 0) ∨ (q = "static") ∨ (q = "self" ∧ c_u ∈ IsTrait)
32:     if q ∈ {"this", "self", "static"} then
33:         if c_u ≠ ⊥ then return METHOD(c_u, s, st, disp) else return BYNAME(s, f, st)
34:     if q begins with "\" and contains none of ":", ".", "(", "$" then
35:         k ← PHPCLASS(q, f)
36:         if k ≠ ⊥ then return METHOD(k, s, st, disp) else return ⊥
37:     return BYNAME(s, f, st)
```

The flag $\mathit{disp}$ marks calls that PHP dispatches at run time (`$this->m()`, `static::m()`, and `self::m()` inside a trait, where `self` denotes the using class). Only these calls may resolve to an override in a subclass (line 12). `X::m()` and `self::m()` outside a trait search upward only, and `parent::m()` starts at the extended class and searches upward only. A known class without the method resolves to its `__call` or `__callStatic` handler. A class that the project does not declare yields no edge, because $\textsc{PhpClass}$ returns $\bot$ and no name-based guess follows (line 36). A method called on a receiver of unknown class resolves only if it is the unique eligible PHP method with that name in the project, and an instance call (`->`) never resolves to a static method (line 17).

## 3.9 Graph construction

Algorithm 6 composes the components above into six phases. Phases 1, 3 (base resolution), 4 and 5 (materialization) run in parallel over consecutive chunks of the canonical file order. The chunk size is $\mathrm{clamp}(\lfloor F / 8P \rfloor, 16, 2048)$ for $P$ worker threads, so that each worker receives about eight chunks.

```text
Algorithm 6  BUILD-CALL-GRAPH(Π; τ0 = 8, ς = true)
 1: if 𝓕 = ∅ then return (∅, ∅)
    ▷ Phase 0: canonical order and global indices (sequential)
 2: (f_0, …, f_{F−1}) ← 𝓕 sorted by (dir(p(f)), p(f));  compute d
 3: build 𝓘 (Section 3.4.3);  if some file is PHP then build the include index (Section 3.4.4)
    ▷ Phase 1: per-file extraction (parallel, order-preserving)
 4: for each file f do in parallel
 5:     if fam(f) = PHP then Inc(f) ← resolved include targets, sorted and deduplicated
 6:     else (N_f, Q_f) ← import environment of f (Section 3.4.3)
 7:     emit node records: Fn_f in order; then, for each c ∈ Cl_f, c followed by Meth(c)
 8:     emit base references (c, β, f, ln(c)) for β ∈ bases(c); for PHP also ext and Tr references
    ▷ Phase 2: merge (sequential, except the include closures)
 9: V ← first occurrence of each identifier in emission order;  derive ι and Id
10: if some Inc(f) ≠ ∅ then compute Inc*(f) for every f (parallel)
11: build B (Section 3.4.1) and 𝓜 (Section 3.4.2)
    ▷ Phase 3: inheritance (must precede calls, since MRO reads Par and Chi)
12: E_inh ← ⟨(Id(id(c)), w, ln) : (c, β, f, ln) a base reference, w ← RB(β, f), w ∉ {⊥, Id(id(c))}⟩
            where RB = PHPCLASS if fam(f) = PHP, else RESOLVE-BASE
13: sort E_inh by (from, to, ln) and keep the first edge of every run with equal (from, to)
14: build Par, Chi from E_inh;  resolve ext, Tr and IsTrait for PHP classes
    ▷ Phase 4: call edges (parallel)
15: for each file f do in parallel
16:     buf ← ⟨⟩
17:     for each declaration x of f (functions first, then the methods of each class) do
18:         u ← Id(id(x));  determine c_u, r_u, ns_u (Section 3.8)
19:         for each κ = (γ, ln, cnd) ∈ Calls(x) do
20:             v ← RESOLVE-PHP(γ, f, c_u, ns_u) if fam(f) = PHP else RESOLVE-GEN(x, γ, f, c_u, r_u)
21:             if v ≠ ⊥ then append (u, v, ln, cnd) to buf
22:     append FLUSH(buf) to E_call
    ▷ Phase 5: materialization
23: indeg(v) ← |{(u, v) ∈ E_call}| for every v
24: return (V with k, φ, entry flag and indeg;  E_inh followed by E_call)

function FLUSH(buf)                   ▷ one edge per (caller, target) and file
    sort buf by (u, v, ln)
    for each maximal run of entries with equal (u, v):
        emit (u, v, ln = minimum line of the run, cnd = conjunction of the run's cnd)
```

Edges are aggregated per file rather than per caller. A file may declare the same identifier twice (for example in `#ifdef` branches or in conditional PHP declarations), and both declarations denote the same node. A separate pass, `populate_called_by_from_graph`, derives each declaration's list of callers by reversing $E_{\mathrm{call}}$ of the materialized graph. It does not repeat resolution and can therefore run after the graph has been stored and reloaded.

## 3.10 Formal properties

**Proposition 1 (determinism and schedule independence).** The node sequence, the edge sequence and all attributes produced by Algorithm 6 are functions of the input $\Pi$ and the options $(\tau_0, \varsigma)$ alone. They do not depend on the iteration order of the input map, on the number of threads $P$, or on the chunk size.

*Proof sketch.* (i) The input is an unordered map from paths to summaries. It is read once and immediately sorted under the total order of Section 3.3.1. (ii) Every subsequent collection is either produced by an order-preserving indexed parallel map (chunk results are concatenated in chunk order, and a file's index is its chunk offset plus its position, which does not depend on the chunk size) or explicitly sorted under a total order ($\mathcal{M}$, $E_{\mathrm{inh}}$, the include closures, the per-file edge buffers). (iii) Every selection is made under a total order or abstains on ties: $\textsc{Pick}$ ends its key with the injective $\iota$; $\mathrm{own}$ takes the minimum $\iota$; $\textsc{Walk}$ follows adjacency lists ordered by $\iota$; include suffix matching returns $\bot$ on ties; import maps keep the first registration. (iv) Within $\textsc{Flush}$, entries that tie on $(u, v, \mathrm{ln})$ differ at most in $\mathrm{cnd}$, and the conjunction is independent of their order. $\square$

**Proposition 2 (family separation).** Every node returned by $\textsc{Pick}$ satisfies $\mathrm{fam}(\varphi(v)) = \mathrm{fam}(f)$ for the caller's file $f$. Under (A1), an edge between a $\textsf{PHP}$ node and a $\textsf{GEN}$ node can therefore arise only from an exact-identifier match (Algorithm 4, lines 4 and 23), or from a hierarchy walk over an inheritance edge that such a match created.

*Proof.* Line 3 of Algorithm 1 discards every candidate of the other family. $\textsc{Nest}$ constructs an identifier with the caller's own path, so under (A1) it returns a node declared in $f$. A method returned by $\mathrm{own}$, $\textsc{Walk}$ or $\textsc{Mro}$ is declared in the file of its owner class, and the classes reached are connected to $c_u$ (declared in $f$) or to a class returned by $\textsc{Pick}$ through inheritance edges, each of which, by the same argument, preserves the family unless it came from an exact-identifier match. $\square$

All $\textsf{GEN}$ languages share a single name space. A call written in one $\textsf{GEN}$ language may therefore resolve to a declaration in another.

**Proposition 3 (bounded ambiguity and abstention).**
(a) If $\textsc{Lookup}$ returns a node selected in tier T4 or T5, then at most $\tau$ candidates were eligible in that tier.
(b) If $N_f(s) = \textsf{ext}$ and $\varsigma = 1$, then $\textsc{Lookup}(s, f, \sigma) = \bot$ whenever T1 yields no result, whatever $B(s)$ contains. The same holds for a qualified $\textsf{GEN}$ call whose root is registered as external (Algorithm 4, line 17).
(c) $\textsc{ByName}$ returns a method only if it is the unique eligible $\textsf{PHP}$ method with that index name in the project.

*Proof.* (a) follows from lines 18 and 21 of Algorithm 2. Line 18 returns the held node $h$, which was selected in T3, when $n > \tau$. (b) follows from line 8 of Algorithm 2, which executes after T1, and from line 17 of Algorithm 4. (c) is line 17 of Algorithm 5. $\square$

**Corollary 1 (precision floor under a uniform model).** Consider a call site resolved in T4 or T5 with $n$ eligible candidates, and suppose that its true target is one of them, each with probability $1/n$. Then any selection rule is correct with probability $1/n \ge 1/\tau$, which is $0.125$ for the default $\tau = 8$.

The cap is therefore a classifier with a reject option in the sense of Chow (1970): it abstains exactly when the posterior probability of the best candidate under the uniform model, $1/n$, falls below $1/\tau$. The corollary does not assume that directory proximity improves on the uniform model; whether it does is an empirical question. The veto of Proposition 3(b) is the limiting case in which the evidence assigns probability zero to every project declaration, and Proposition 3(c) is the strictest setting, $\tau = 1$. The tiers T1–T3 are not capped. A same-file, imported, included or same-directory candidate is selected regardless of how many other declarations share its name, so the design treats locality as sufficient evidence.

**Proposition 4 (edge multiplicity).** For each file and each ordered pair $(u, v)$, $\textsc{Flush}$ emits at most one call edge. Under (A1), all call sites of a node lie in one file, so $E_{\mathrm{call}}$ contains at most one edge per ordered pair, and $\mathrm{indeg}(v)$ equals the number of distinct callers of $v$ (not the number of call sites). Self-loops are retained in $E_{\mathrm{call}}$ (recursion) and excluded from $E_{\mathrm{inh}}$, which also contains at most one edge per ordered pair. Each retained inheritance edge carries the smallest declaration line among its duplicates.

*Proof.* Immediate from $\textsc{Flush}$ and from lines 12–13 of Algorithm 6. $\square$

**Proposition 5 (termination of hierarchy queries).** Every call of $\textsc{Walk}$ and $\textsc{Traits}$ examines at most $K$ classes, including the start class, and $\textsc{Up}$ follows at most $K$ `extends` steps. All hierarchy queries therefore terminate, also on cyclic inheritance graphs, and the number of classes they examine is bounded independently of the input.

*Proof.* The visited list grows by one element per non-duplicate dequeue and the procedure returns once its length exceeds $K$. The loop in $\textsc{Up}$ is bounded by $K$ iterations. $\square$

**Remark 1 (method resolution order).** $\textsc{Mro}$ is a bounded breadth-first search and not a language-specific linearization. Under single inheritance, the ancestor walk visits classes in the order of the inheritance chain. Under multiple inheritance its order can differ from C3 linearization (Barrett et al., 1996). For example, for `class D(B, C)` with `B(A)`, where both `A` and `C` define `foo`, C3 yields `A.foo`, whereas $\textsc{Mro}$ finds `C.foo` at distance one. The descendant walk returns one override, the nearest one, whereas class hierarchy analysis (Dean et al., 1995) admits every override as a possible target.

**Remark 2 (soundness and precision).** PRISMv2 is unsound, in that it misses edges of $C^{\star}$. Sources are: abstention (the cap, the veto and the uniqueness rule for PHP receivers); single-target resolution, so that a dynamically dispatched call contributes at most one edge; calls through computed names, which the PHP rules reject explicitly (`$this->$m()`, `$fn()`); and call sites that the front-ends do not emit. PRISMv2 is also imprecise, in that it can emit edges outside $C^{\star}$. Sources are: locality-based selection without a cap in T1–T3; the shared $\textsf{GEN}$ name space; the breadth-first hierarchy order of Remark 1; and the proximity heuristic in T4.

## 3.11 Complexity analysis

**Table 3.** Parameters of the analysis.

| Symbol | Meaning |
| --- | --- |
| $F$, $F_{P}$ | number of files; number of PHP files |
| $N_0$, $N$ | number of emitted declarations; number of nodes, $N = \lvert V \rvert \le N_0$ |
| $M$ | size of the method index, $M = \lvert \mathcal{M} \rvert \le N$ |
| $C$, $R$ | number of call sites; number of base references (including PHP `extends` and trait references) |
| $J_G$, $J_P$ | number of $\textsf{GEN}$ import records plus imported items; number of PHP include directives |
| $L$ | maximum length in bytes of a path, identifier or callee string |
| $D$ | maximum number of components of a path |
| $b(s)$ | $\lvert B(s) \rvert$, the number of declarations indexed under $s$ |
| $\Delta$ | maximum number of direct bases, subclasses or used traits of a class |
| $\Delta_I$ | maximum number of direct includes of a PHP file |
| $\mu$ | maximum number of files that share a base name |
| $\pi_{\max}$ | maximum number of parameters of a declaration |
| $K = 64$, $X = 2048$ | hierarchy budget; include-closure cap |
| $P$ | number of worker threads |

Hash-map operations on keys of length at most $L$ cost $O(L)$ expected time. All bounds below are expected bounds with respect to hashing.

**Lemma 4 (cost of a lookup).** $\textsc{Lookup}(s, f, \sigma)$ runs in $O\bigl(L + b(s)\,(L + \log F)\bigr)$ time, and so does $\textsc{LookupIn}$.

*Proof.* Retrieving $B(s)$ and probing $N_f$ cost $O(L)$. T1 and T3 perform two binary searches each (Lemma 2) and scan an interval of at most $b(s)$ candidates without proximity (Lemma 3). T2 and T2b test each of the at most $b(s)$ candidates for membership in a sorted file list of size at most $F$ in $O(\log F)$ and compute $\lambda$ in $O(L)$ for each eligible one. T4 and T5 scan at most $b(s)$ candidates, computing $\lambda$ for each. The number of tiers is constant. $\square$

T4 has no early exit. An abstaining lookup computes $\lambda$ for every eligible candidate even after the count has exceeded $\tau$, so its cost is $\Theta(b(s)\,L)$ and not $O(\tau L)$.

**Lemma 5 (cost of hierarchy queries).** $\textsc{Walk}$ and $\textsc{Traits}$ run in $O(K^{2}\Delta + K L \log M)$ time, and so does $\textsc{Mro}$. $\textsc{Up}$ and $\textsc{Method}$ run in $O(K^{3}\Delta + K^{2} L \log M)$ time.

*Proof.* A walk enqueues the at most $\Delta$ neighbors of each of the at most $K$ classes it expands, so it performs $O(K\Delta)$ dequeues. Each dequeue tests membership in a visited list of length at most $K$ in $O(K)$. Each of the at most $K$ visited classes costs one binary search in $\mathcal{M}$, that is $O(L \log M)$. $\textsc{Up}$ performs at most $K$ chain steps, each consisting of one $\mathrm{own}$ query and one $\textsc{Traits}$ query, followed by one walk. $\textsc{Method}$ calls $\textsc{Find}$ at most twice, and each call performs one $\textsc{Up}$ and at most one walk. $\square$

Since $K$ is a constant, every hierarchy query costs $O(\Delta + L \log M)$ in terms of the input.

**Per-call-site cost.** By Lemmas 4 and 5, a $\textsf{GEN}$ call site with name $s$ costs

$$O\bigl(\pi_{\max} L + (b(s) + b(\hat q))(L + \log F) + K^{2}\Delta + K L \log M\bigr),$$

where $\hat q = \textsc{Last}(q)$ if line 18 of Algorithm 4 executes and $b(\hat q) = 0$ otherwise. Algorithm 4 performs at most three lookups and at most two hierarchy queries per call site. A $\textsf{PHP}$ call site costs $O\bigl(L + (b(s) + b(\hat q))(L + \log F) + K^{3}\Delta + K^{2} L \log M\bigr)$, where $\hat q$ is the qualifier resolved by $\textsc{PhpClass}$, if any. A base reference costs $O(L + b(s)(L + \log F))$. We call the sum

$$\Phi = \sum_{\text{call sites } \kappa} \bigl(b(s_\kappa) + b(\hat q_\kappa)\bigr) + \sum_{\text{base refs } \beta} b(s_\beta)$$

the *collision mass* of the input. Grouping the call sites by name gives $\Phi \ge \sum_{s} c(s)\, b(s)$, where $c(s)$ is the number of call sites that name $s$. $\Phi$ is large when frequently called names are also frequently declared; the implementation itself names `Get`, `Close` and `String` as examples.

**Theorem 1 (total work).** The total work of Algorithm 6 is

$$
\begin{aligned}
W = O\Bigl(\ & L F \log F \;+\; F D L \;+\; N_0 L \;+\; M L \log M \;+\; R \log R \;+\; C \log C \\
& +\; J_G D L \;+\; J_P\, \mu L \;+\; F_P\, X (\Delta_I + \log X) \\
& +\; (C + R)\bigl(\pi_{\max} L + K^{3}\Delta + K^{2} L \log M\bigr) \;+\; \Phi\,(L + \log F) \;+\; (N + C + R) L \ \Bigr).
\end{aligned}
$$

*Proof.* The terms correspond to the phases of Algorithm 6, in order: sorting the files with $O(L)$ comparisons; the module index, in which each file contributes at most $2D$ keys; extraction and first-wins merging; sorting $\mathcal{M}$; sorting $E_{\mathrm{inh}}$; the per-file $\textsc{Flush}$ sorts, $\sum_f C_f \log C_f \le C \log C$; module resolution, which probes at most $D$ suffixes per import; include resolution, which scores at most $\mu$ same-named files per directive; the include closures, each a breadth-first search over at most $X$ files followed by a sort; the per-site constants of Lemmas 4 and 5 together with the shadowing test; the per-site lookup costs summed into $\Phi$; and materialization of the at most $N + C + R$ output records. $\square$

**Corollary 2 (asymptotic regimes).** Treat $L$, $D$, $\Delta$, $\Delta_I$, $\mu$ and $\pi_{\max}$ as bounded, and recall that $K$ and $X$ are constants. Let $n = F + N_0 + C + R + J_G + J_P$. Then

$$W = O\bigl(F \log F + M \log M + R \log R + C \log C + (C + R)\log M + \Phi \log F + n\bigr).$$

1. **Worst case.** $\Phi \le (2C + R)\,N$, so $W = O(n^{2} \log n)$. The bound is attained up to the logarithmic factor when $\Theta(N)$ declarations share one name and $\Theta(C)$ call sites use it.
2. **Bounded name multiplicity.** If $b(s) \le \beta$ for every $s$ and a constant $\beta$, then $\Phi \le (2C + R)\beta$ and $W = O(n \log n)$.

**Theorem 2 (space).** In addition to the input summaries, which the resolver borrows without copying, Algorithm 6 uses

$$S = O\bigl(F D + N_0 + M + R + J_G + J_P + C + F_P \min(F, X) + N_P L + (N + C + R) L\bigr)$$

space, where $N_P$ is the number of PHP nodes, whose lowercase index names are stored as owned strings. The terms account, in order, for the module index; the extracted node records, $\mathrm{Id}$ and $B$; $\mathcal{M}$; the base references, $E_{\mathrm{inh}}$, $\mathrm{Par}$, $\mathrm{Chi}$ and the PHP hierarchy; the import environments and include sets; the raw call edges; the include closures; the PHP names; and the materialized output. Each worker additionally needs $O(K\Delta + C_f)$ transient space for breadth-first queues and the per-file edge buffer, where $C_f$ is the number of call sites in the file being processed.

**Parallel time.** Write $W = W_{\mathrm{par}} + W_{\mathrm{seq}}$, where the sequential part comprises file sorting, the module and include indices, the merge, the construction of $B$ and $\mathcal{M}$, the sort of $E_{\mathrm{inh}}$ and the in-degree count:

$$W_{\mathrm{seq}} = O\bigl(L F \log F + F D L + N_0 L + M L \log M + R \log R + C\bigr).$$

The dominant term $\Phi (L + \log F)$ falls in $W_{\mathrm{par}}$. Under work-stealing scheduling, the running time on $P$ threads is

$$T_P = O\bigl(W_{\mathrm{par}} / P + W_{\mathrm{seq}} + w_{\max}\bigr),$$

where $w_{\max}$ is the work of the most expensive chunk, which contains at most 2048 files.

## 3.12 Fidelity of the formalization to the current implementation

Sections 3.3–3.9 formalize the resolver as written. Three properties of the current implementation and of its front-ends determine which parts of the procedure are exercised in practice. We established them by reading the source and by executing the implementation on minimal programs in Python, Go, Rust, TypeScript, JavaScript, Java and C++.

**(F1) Index names of $\textsf{GEN}$ methods.** The resolver indexes $\textsf{GEN}$ nodes by $\eta(v) = \mathrm{bare}(\mathrm{id}(v))$, and $\mathrm{bare}$ strips only a `.`-separated qualifier. The $\textsf{GEN}$ front-ends, however, emit method identifiers of the form `method_C_n::p`, for which $\mathrm{bare}$ yields `C_n` rather than the declared name `n`. Consequently, $B(n)$ contains no $\textsf{GEN}$ method under its declared name; $\mathcal{M}$ contains no triple $(c, n, \cdot)$; $\mathrm{own}$, $\textsc{Walk}$ and $\textsc{Mro}$ return $\bot$ for every call-site name that does not literally spell `C_n`; and the $\textsf{Me}$ column of Table 1 never applies to a $\textsf{GEN}$ call. In the current implementation, a $\textsf{GEN}$ call edge can target a method node only through an exact-identifier match. None of the graphs produced from our minimal programs contains a call edge into a method node. The repository documents this behavior as a known defect (`docs/known-issues.md`). $\textsf{PHP}$ is unaffected, because PHP methods are indexed by their lowercased declared name. The procedure of Sections 3.5–3.8 does not depend on the choice of $\eta$, and the intended index name for $\textsf{GEN}$ methods is the declared name $\mathrm{nm}(v)$, as for $\textsf{PHP}$. Empirical results must state which $\eta$ was in effect.

**(F2) Callee strings.** The qualified branches of Algorithm 4 (lines 11–21) presuppose that $\gamma$ retains the receiver or qualifier. The JavaScript and PHP front-ends retain it (`this.save`, `c.run`, `util.fmt`). The Python, Go, TypeScript, Rust and Java front-ends emit only the last segment: `self.save()` and `c.run()` arrive as `save` and `run`, Rust `S::new()` as `new`, and Java `Util.fmt()` as `fmt`. The C++ front-end retained the qualifier of `s.get()` but not that of `this->inner()`. For front-ends in the second group, every call reaches line 7 of Algorithm 4 as a bare call. The self-qualifier branch, qualifier-based import resolution, class-qualified resolution and the external veto for qualifiers are therefore inactive for them, while tier T2 for bare imported names remains active. Java constructor calls arrive as `new C`, which $\textsc{Split}$ leaves undivided and which matches no index name, because only the PHP path interprets the `new ` prefix. In addition, the JavaScript front-end recorded no base class for `class Child extends Base` in our test program, so no inheritance edge was created for it.

**(F3) Receiver flag.** $r_u = 1$ requires $\pi_1(x) \in \mathrm{SELF}$ verbatim. The Python front-end omits `self` and `cls` from $\pi(x)$. The Go, Java, C++, JavaScript and TypeScript front-ends list no implicit receiver. The Rust front-end records `&self` verbatim, which does not match, and records a by-value receiver as `self`, which does. Among the tested front-ends, lines 8–9 of Algorithm 4 are therefore reachable only from Rust methods with a by-value `self` receiver, and by (F1) they currently yield no edge.

## References

Barrett, K., Cassels, B., Haahr, P., Moon, D. A., Playford, K., & Withington, P. T. (1996). A monotonic superclass linearization for Dylan. In *Proceedings of the 11th ACM SIGPLAN Conference on Object-Oriented Programming, Systems, Languages, and Applications (OOPSLA '96)*. ACM. [DOI to be confirmed in the ACM Digital Library: both 10.1145/236337.236343 and 10.1145/236338.236343 are in circulation.]

Chow, C. K. (1970). On optimum recognition error and reject tradeoff. *IEEE Transactions on Information Theory, 16*(1), 41–46. https://doi.org/10.1109/TIT.1970.1054406

Dean, J., Grove, D., & Chambers, C. (1995). Optimization of object-oriented programs using static class hierarchy analysis. In *ECOOP'95: Object-Oriented Programming, 9th European Conference* (Lecture Notes in Computer Science, Vol. 952, pp. 77–101). Springer. https://doi.org/10.1007/3-540-49538-X_5
