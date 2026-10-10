abstract class A {
  abstract get(): i32;
}
class B extends A {
  get(): i32 { return 11; }
}
function main(): i32 {
  const b = new B();
  console.log(b.get());
  return 0;
}
