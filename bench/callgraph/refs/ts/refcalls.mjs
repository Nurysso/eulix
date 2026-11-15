// Reference call sites for TypeScript / JavaScript, from the TypeScript checker.
//
// Usage: node refcalls.mjs <root>   (JSON lines on stdout)
//
// All .ts/.tsx/.js/.jsx/.mjs/.cjs files under the root (no node_modules, no
// .d.ts) go into one program with allowJs, so imports and require() between
// project files resolve. For each call / new expression the checker's resolved
// signature gives the declaration. A call to a class or interface member also
// lists every project method of that name in subclasses / implementors (class
// hierarchy analysis). Declarations in lib.d.ts or node_modules are "external";
// a callback parameter or other value with no function body is "unknown".
import ts from "typescript";
import fs from "node:fs";
import path from "node:path";

const root = fs.realpathSync(path.resolve(process.argv[2]));
const EXT = /\.(ts|tsx|js|jsx|mjs|cjs|mts|cts)$/;
const files = [];
(function walk(dir) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    if (e.name === "node_modules" || e.name.startsWith(".")) continue;
    const p = path.join(dir, e.name);
    if (e.isDirectory()) walk(p);
    else if (EXT.test(e.name) && !/\.d\.[mc]?ts$/.test(e.name)) files.push(p);
  }
})(root);

const program = ts.createProgram(files, {
  allowJs: true, checkJs: false, noEmit: true, skipLibCheck: true, strict: false,
  target: ts.ScriptTarget.ESNext, module: ts.ModuleKind.ESNext,
  moduleResolution: ts.ModuleResolutionKind.Bundler, jsx: ts.JsxEmit.Preserve,
  experimentalDecorators: true, emitDecoratorMetadata: false, resolveJsonModule: true,
  allowImportingTsExtensions: true, types: [],
});
const checker = program.getTypeChecker();

const inProject = (sf) => {
  const f = sf.fileName;
  return !f.includes("/node_modules/") && !sf.isDeclarationFile && f.startsWith(root + "/");
};
const rel = (sf) => path.relative(root, sf.fileName).split(path.sep).join("/");
const lineOf = (sf, pos) => sf.getLineAndCharacterOfPosition(pos).line + 1;

const BODY = new Set([
  ts.SyntaxKind.FunctionDeclaration, ts.SyntaxKind.MethodDeclaration, ts.SyntaxKind.Constructor,
  ts.SyntaxKind.ArrowFunction, ts.SyntaxKind.FunctionExpression, ts.SyntaxKind.GetAccessor,
  ts.SyntaxKind.SetAccessor,
]);
const MEMBER = new Set([ts.SyntaxKind.MethodDeclaration, ts.SyntaxKind.MethodSignature, ts.SyntaxKind.PropertyDeclaration, ts.SyntaxKind.PropertySignature]);

function declName(d) {
  if (d.kind === ts.SyntaxKind.Constructor) return d.parent.name?.text ?? "constructor";
  if (d.name && ts.isIdentifier(d.name)) return d.name.text;
  if (d.name) return d.name.getText();
  const p = d.parent; // const f = () => {}
  if (p && ts.isVariableDeclaration(p) && ts.isIdentifier(p.name)) return p.name.text;
  if (p && ts.isPropertyAssignment(p)) return p.name.getText();
  return "";
}
function targetOf(d) {
  const sf = d.getSourceFile();
  const pos = d.name ? d.name.getStart(sf) : d.getStart(sf);
  return { file: rel(sf), line: lineOf(sf, pos), name: declName(d) };
}

// class/interface symbol -> project classes below it
const below = new Map();
function ancestors(type, seen = new Set()) {
  const out = [];
  if (!type?.symbol || !type.isClassOrInterface?.()) return out;
  let bases = [];
  try { bases = checker.getBaseTypes(type) ?? []; } catch { return out; }
  for (const b of bases) {
    const s = b.symbol ?? b.aliasSymbol;
    if (!s || seen.has(s)) continue;
    seen.add(s); out.push(s);
    if (b.isClassOrInterface?.()) out.push(...ancestors(b, seen));
  }
  return out;
}
const classes = [];
for (const sf of program.getSourceFiles()) {
  if (!inProject(sf)) continue;
  ts.forEachChild(sf, function v(n) {
    if ((ts.isClassDeclaration(n) || ts.isClassExpression(n)) && n.name) {
      const sym = checker.getSymbolAtLocation(n.name);
      if (sym) {
        classes.push(n);
        const anc = new Set(ancestors(checker.getDeclaredTypeOfSymbol(sym)));
        for (const h of n.heritageClauses ?? []) {
          for (const t of h.types) {
            let s = checker.getSymbolAtLocation(t.expression);
            if (s && s.flags & ts.SymbolFlags.Alias) s = checker.getAliasedSymbol(s);
            if (s) { anc.add(s); for (const a of ancestors(checker.getDeclaredTypeOfSymbol(s))) anc.add(a); }
          }
        }
        for (const a of anc) { if (!below.has(a)) below.set(a, []); below.get(a).push(n); }
      }
    }
    ts.forEachChild(n, v);
  });
}
function overriders(decl) {
  const owner = decl.parent;
  if (!owner || !(ts.isClassLike(owner) || ts.isInterfaceDeclaration(owner)) || !owner.name) return [];
  const sym = checker.getSymbolAtLocation(owner.name);
  const name = declName(decl);
  const out = [];
  for (const c of below.get(sym) ?? []) {
    for (const m of c.members) {
      const method = ts.isMethodDeclaration(m) && m.body;
      const fnField = ts.isPropertyDeclaration(m) && m.initializer && ts.isFunctionLike(m.initializer);
      if ((method || fnField) && declName(m) === name) out.push(m);
    }
  }
  return out;
}

function resolve(node) {
  const sig = checker.getResolvedSignature(node);
  let d = sig?.declaration;
  if (!d || ts.isJSDocSignature?.(d)) {
    let expr = node.expression;
    if (ts.isPropertyAccessExpression(expr)) expr = expr.name;
    let s = expr && checker.getSymbolAtLocation(expr);
    if (s && s.flags & ts.SymbolFlags.Alias) s = checker.getAliasedSymbol(s);
    d = s?.valueDeclaration ?? s?.declarations?.[0];
  }
  return d;
}

const out = [];
for (const sf of program.getSourceFiles()) {
  if (!inProject(sf)) continue;
  const file = rel(sf);
  ts.forEachChild(sf, function visit(node) {
    const isCall = ts.isCallExpression(node), isNew = ts.isNewExpression(node);
    if (isCall || isNew) {
      let expr = node.expression;
      while (ts.isParenthesizedExpression(expr) || ts.isNonNullExpression(expr)) expr = expr.expression;
      let nameNode = null, name = "";
      if (ts.isIdentifier(expr)) nameNode = expr;
      else if (ts.isPropertyAccessExpression(expr)) nameNode = expr.name;
      else if (expr.kind === ts.SyntaxKind.SuperKeyword) nameNode = expr;
      if (nameNode && !(isCall && expr.kind === ts.SyntaxKind.ImportKeyword)) {
        name = nameNode.getText(sf);
        const rec = {
          file, line: lineOf(sf, nameNode.getStart(sf)), col: sf.getLineAndCharacterOfPosition(nameNode.getStart(sf)).character + 1,
          lines: [lineOf(sf, node.getStart(sf))], name,
          kind: isNew || expr.kind === ts.SyntaxKind.SuperKeyword ? "constructor" : ts.isPropertyAccessExpression(expr) ? "method" : "function",
          status: "unknown", dispatch: "static", targets: [],
        };
        let d;
        try { d = resolve(node); } catch { d = undefined; }
        if (d) {
          const dsf = d.getSourceFile();
          if (!inProject(dsf)) rec.status = "external";
          else {
            if (ts.isClassLike(d)) { rec.kind = "constructor"; rec.targets.push(targetOf(d)); }
            else if (BODY.has(d.kind) && (d.body || d.kind === ts.SyntaxKind.ArrowFunction)) rec.targets.push(targetOf(d));
            else if (d.kind === ts.SyntaxKind.MethodSignature || (ts.isMethodDeclaration(d) && !d.body)) rec.targets.push(targetOf(d)); // interface / abstract
            if (MEMBER.has(d.kind) || ts.isMethodDeclaration(d)) {
              if (d.kind === ts.SyntaxKind.MethodDeclaration || d.kind === ts.SyntaxKind.MethodSignature) rec.kind = "method";
              const isStatic = d.modifiers?.some((m) => m.kind === ts.SyntaxKind.StaticKeyword);
              const isPrivate = d.modifiers?.some((m) => m.kind === ts.SyntaxKind.PrivateKeyword) || d.name?.kind === ts.SyntaxKind.PrivateIdentifier;
              if (!isStatic && !isPrivate) {
                const extra = overriders(d);
                if (extra.length) rec.dispatch = "virtual";
                for (const m of extra) rec.targets.push(targetOf(m));
              }
            }
            if (rec.targets.length) rec.status = "project";
          }
        }
        out.push(JSON.stringify(rec));
      }
    }
    ts.forEachChild(node, visit);
  });
}
process.stdout.write(out.join("\n") + (out.length ? "\n" : ""));
