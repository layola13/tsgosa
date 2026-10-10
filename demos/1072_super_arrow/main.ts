class A {
  v(): i32 { return 10; }
}
class B extends A {
  v(): i32 {
    const f = () => super.v() + 1;
    return f();
  }
}
function main(): i32 {
  const b = new B();
  console.log(b.v());
  return 0;
}
