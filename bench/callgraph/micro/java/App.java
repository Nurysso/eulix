public class App {
    void bar() { baz(); }
    void baz() {}
    static void run() {
        App f = new App();
        f.bar();
        util();
    }
    static void util() {}
}
abstract class Shape { abstract int area(); int twice() { return area() * 2; } }
class Sq extends Shape { int area() { return 4; } }
class Uses { int go(Shape s) { java.util.List<String> l = new java.util.ArrayList<>(); l.add("x"); Missing.call(); return s.area() + s.toString().length(); } }
