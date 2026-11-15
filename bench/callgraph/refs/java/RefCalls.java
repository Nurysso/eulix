// Reference call sites for Java sources, from javac's own attribution.
//
// Usage: java RefCalls.java <source root>   (JSON lines on stdout)
//
// Needs only a JDK. All .java files under the root are compiled together with
// no classpath, so calls into missing dependencies come out "unknown". javac is
// told to keep attributing after errors.
//
// A call to a project method lists that method plus every project method that
// overrides it (class hierarchy analysis), unless the method is static, private,
// final or a constructor. Calls on types from outside the project are "external".
import com.sun.source.tree.*;
import com.sun.source.util.*;
import java.io.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.*;
import java.util.stream.*;
import javax.lang.model.element.*;
import javax.lang.model.type.TypeMirror;
import javax.lang.model.util.Elements;
import javax.tools.*;

@SuppressWarnings("removal")
public class RefCalls {
    static Path root;
    static Trees trees;
    static Elements elements;
    static javax.lang.model.util.Types types;
    static final Map<String, List<ExecutableElement>> methodsByName = new HashMap<>();
    static final Map<ExecutableElement, List<ExecutableElement>> overriders = new HashMap<>();
    static final StringBuilder out = new StringBuilder();

    public static void main(String[] args) throws Exception {
        root = Path.of(args[0]).toRealPath();
        List<File> files;
        try (Stream<Path> s = Files.walk(root)) {
            files = s.filter(p -> p.toString().endsWith(".java") && !p.getFileName().toString().equals("module-info.java"))
                    .map(Path::toFile).collect(Collectors.toList());
        }
        JavaCompiler jc = ToolProvider.getSystemJavaCompiler();
        int[] errors = {0};
        DiagnosticListener<JavaFileObject> diag = d -> { if (d.getKind() == Diagnostic.Kind.ERROR) errors[0]++; };
        StandardJavaFileManager fm = jc.getStandardFileManager(diag, null, StandardCharsets.UTF_8);
        List<String> opts = List.of("-proc:none", "-implicit:none", "-nowarn", "-Xlint:none", "-XDshould-stop.at=FLOW",
                "-XDshould-stop.ifError=FLOW", "-XDshould-stop.ifNoError=FLOW", "-Xmaxerrs", "1000000", "-sourcepath", root.toString());
        JavacTask task = (JavacTask) jc.getTask(null, fm, diag, opts, null, fm.getJavaFileObjectsFromFiles(files));
        trees = Trees.instance(task);
        elements = task.getElements();
        types = task.getTypes();
        List<CompilationUnitTree> units = new ArrayList<>();
        task.parse().forEach(units::add);
        task.analyze();
        if (errors[0] > 0) System.err.println("java-ref: " + errors[0] + " compile errors (unresolved calls are 'unknown')");

        for (CompilationUnitTree u : units) {
            new TreePathScanner<Void, Void>() {
                @Override public Void visitMethod(MethodTree m, Void v) {
                    Element e = trees.getElement(getCurrentPath());
                    if (e instanceof ExecutableElement ee) methodsByName.computeIfAbsent(ee.getSimpleName().toString(), k -> new ArrayList<>()).add(ee);
                    return super.visitMethod(m, v);
                }
            }.scan(u, null);
        }
        for (CompilationUnitTree u : units) scanCalls(u);
        System.out.print(out);
    }

    static String rel(CompilationUnitTree u) {
        Path p = Path.of(u.getSourceFile().toUri());
        try { p = p.toRealPath(); } catch (IOException ignored) {}
        return p.startsWith(root) ? root.relativize(p).toString().replace('\\', '/') : null;
    }

    static int line(CompilationUnitTree u, long pos) { return (int) u.getLineMap().getLineNumber(pos); }

    /** Where the reference points for a method: the line of its body (or of its end when abstract). */
    static Map<String, Object> targetOf(ExecutableElement e) {
        TreePath p = trees.getPath(e);
        if (p == null) return null;
        CompilationUnitTree u = p.getCompilationUnit();
        String f = rel(u);
        if (f == null) return null;
        SourcePositions sp = trees.getSourcePositions();
        Tree t = p.getLeaf();
        long pos = sp.getStartPosition(u, t);
        if (t instanceof MethodTree mt) {
            pos = mt.getBody() != null ? sp.getStartPosition(u, mt.getBody()) : sp.getEndPosition(u, mt);
        }
        String name = e.getKind() == ElementKind.CONSTRUCTOR ? e.getEnclosingElement().getSimpleName().toString() : e.getSimpleName().toString();
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("file", f); m.put("line", line(u, pos)); m.put("name", name);
        return m;
    }

    static boolean dispatched(ExecutableElement e) {
        Set<Modifier> mods = e.getModifiers();
        return e.getKind() == ElementKind.METHOD && !mods.contains(Modifier.STATIC) && !mods.contains(Modifier.PRIVATE)
                && !mods.contains(Modifier.FINAL) && !e.getEnclosingElement().getModifiers().contains(Modifier.FINAL);
    }

    static List<ExecutableElement> overridersOf(ExecutableElement m) {
        return overriders.computeIfAbsent(m, k -> {
            List<ExecutableElement> r = new ArrayList<>();
            for (ExecutableElement c : methodsByName.getOrDefault(k.getSimpleName().toString(), List.of())) {
                if (c != k && c.getEnclosingElement() instanceof TypeElement te && elements.overrides(c, k, te)) r.add(c);
            }
            return r;
        });
    }

    static void scanCalls(CompilationUnitTree u) {
        String file = rel(u);
        if (file == null) return;
        SourcePositions sp = trees.getSourcePositions();
        final CharSequence src;
        try { src = u.getSourceFile().getCharContent(true); } catch (IOException ex) { return; }
        new TreePathScanner<Void, Void>() {
            /** The static type of the call's receiver, when it is a project class or interface. */
            TypeElement projectReceiver(ExpressionTree sel) {
                TypeMirror t = null;
                if (sel instanceof MemberSelectTree ms) {
                    TreePath p = new TreePath(new TreePath(getCurrentPath(), ms), ms.getExpression());
                    t = trees.getTypeMirror(p);
                } else {
                    for (TreePath p = getCurrentPath(); p != null; p = p.getParentPath()) {
                        if (p.getLeaf() instanceof ClassTree) {
                            Element e = trees.getElement(p);
                            if (e != null) t = e.asType();
                            break;
                        }
                    }
                }
                if (t == null || !(types.asElement(t) instanceof TypeElement te)) return null;
                return trees.getPath(te) != null ? te : null;
            }

            void emit(Tree call, Tree nameTree, String name, boolean ctor) {
                emit(call, nameTree, name, ctor, null);
            }

            void emit(Tree call, Tree nameTree, String name, boolean ctor, TypeElement recv) {
                long start = sp.getStartPosition(u, call);
                long namePos = nameTree instanceof MemberSelectTree ms ? sp.getEndPosition(u, ms) - ms.getIdentifier().length() : sp.getStartPosition(u, nameTree);
                if (call instanceof NewClassTree && namePos >= 0) { // `new a.b.Name<T>(..)`: point at Name
                    int i = src.toString().indexOf(name, (int) namePos);
                    if (i >= 0 && i - namePos < 400) namePos = i;
                }
                // javac inserts implicit super() calls; they have no text of their own
                if (namePos < 0 || namePos + name.length() > src.length()
                        || !src.subSequence((int) namePos, (int) namePos + name.length()).toString().equals(name)) return;
                Element e = trees.getElement(getCurrentPath());
                String status, kind = ctor ? "constructor" : "method", dispatch = "static", why = null;
                List<Map<String, Object>> targets = new ArrayList<>();
                if (!(e instanceof ExecutableElement ee) || e.asType().getKind() == javax.lang.model.type.TypeKind.ERROR) {
                    status = "unknown";
                } else {
                    Map<String, Object> t0 = targetOf(ee);
                    if (t0 == null && recv != null && dispatched(ee)) {
                        // A method declared outside the project (Object.hashCode)
                        // called on a project type: it can only reach overrides in
                        // subtypes of that type (class hierarchy analysis).
                        for (ExecutableElement c : methodsByName.getOrDefault(ee.getSimpleName().toString(), List.of())) {
                            if (c.getEnclosingElement() instanceof TypeElement owner && elements.overrides(c, ee, owner)
                                    && types.isSubtype(types.erasure(owner.asType()), types.erasure(recv.asType()))) {
                                Map<String, Object> t = targetOf(c);
                                if (t != null) targets.add(t);
                            }
                        }
                        if (targets.isEmpty()) {
                            status = "external";
                        } else {
                            status = "project";
                            dispatch = "virtual";
                        }
                    } else if (t0 == null) {
                        status = "external";
                        // An outside method (Object.toString, List.get) that project
                        // classes override: through an outside receiver type the call
                        // may run project code or not.
                        if (dispatched(ee) && !overridersOf(ee).isEmpty()) {
                            status = "unknown";
                            why = "outside method with project overrides";
                        }
                    } else {
                        if (ee.getModifiers().contains(Modifier.STATIC)) kind = ctor ? "constructor" : "function";
                        targets.add(t0); // abstract / interface declarations count as a static target too
                        if (dispatched(ee)) {
                            for (ExecutableElement o : overridersOf(ee)) {
                                Map<String, Object> t = targetOf(o);
                                if (t != null) { targets.add(t); dispatch = "virtual"; }
                            }
                        }
                        status = targets.isEmpty() ? "unknown" : "project";
                    }
                }
                int ln = line(u, namePos);
                out.append("{\"file\":").append(q(file)).append(",\"line\":").append(ln)
                   .append(",\"col\":").append(u.getLineMap().getColumnNumber(namePos))
                   .append(",\"lines\":[").append(line(u, start)).append("],\"name\":").append(q(name))
                   .append(",\"kind\":\"").append(kind).append("\",\"status\":\"").append(status)
                   .append("\",\"dispatch\":\"").append(dispatch).append("\",\"targets\":[");
                for (int i = 0; i < targets.size(); i++) {
                    Map<String, Object> t = targets.get(i);
                    if (i > 0) out.append(',');
                    out.append("{\"file\":").append(q((String) t.get("file"))).append(",\"line\":").append(t.get("line"))
                       .append(",\"name\":").append(q((String) t.get("name"))).append('}');
                }
                out.append(']');
                if (why != null) out.append(",\"why\":").append(q(why));
                out.append("}\n");
            }

            @Override public Void visitMethodInvocation(MethodInvocationTree node, Void v) {
                ExpressionTree sel = node.getMethodSelect();
                String name = sel instanceof MemberSelectTree ms ? ms.getIdentifier().toString()
                        : sel instanceof IdentifierTree id ? id.getName().toString() : "";
                if (!name.equals("this") && !name.equals("super")) emit(node, sel, name, false, projectReceiver(sel));
                else emit(node, sel, name, true);
                return super.visitMethodInvocation(node, v);
            }

            @Override public Void visitNewClass(NewClassTree node, Void v) {
                if (node.getClassBody() == null) {
                    Tree id = node.getIdentifier();
                    String name = id instanceof ParameterizedTypeTree pt ? pt.getType().toString() : id.toString();
                    name = name.substring(name.lastIndexOf('.') + 1);
                    emit(node, id, name, true);
                }
                return super.visitNewClass(node, v);
            }
        }.scan(u, null);
    }

    static String q(String s) {
        StringBuilder b = new StringBuilder("\"");
        for (char c : s.toCharArray()) {
            if (c == '"' || c == '\\') b.append('\\').append(c);
            else if (c < 0x20) b.append(String.format("\\u%04x", (int) c));
            else b.append(c);
        }
        return b.append('"').toString();
    }
}
