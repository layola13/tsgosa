class A {
  x: i32;
  constructor(x: i32) { this.x = x; }
  get(): i32 { return this.x; }
}
class B extends A {
  constructor(x: i32) { super(x); }
  get(): i32 { return this.x * 2; }
}
function main(): i32 {
  const b = new B(21);
  console.log(b.get());
  return 0;
}
