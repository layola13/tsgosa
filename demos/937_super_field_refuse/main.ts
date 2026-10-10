class A {
  v: i32;
  constructor(v: i32) { this.v = v; }
}
class B extends A {
  constructor(v: i32) { super(v); }
  read(): i32 { return super.v + 1; }
}
function main(): i32 {
  const b = new B(41);
  console.log(b.read());
  return 0;
}
