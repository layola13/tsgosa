class A {
  greet(): string { return "hi"; }
}
class B extends A {
  greet(): string { return super.greet() + "!"; }
}
function main(): i32 {
  const b = new B();
  console.log(b.greet());
  return 0;
}
