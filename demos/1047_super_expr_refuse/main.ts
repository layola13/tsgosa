class A {
  x: i32;
  y: i32;
  constructor(x: i32, y: i32) { this.x = x; this.y = y; }
}
class B extends A {
  constructor(v: i32) { super(v, v * 2); }
}
function main(): i32 {
  const b = new B(5);
  console.log(b.x + b.y);
  return 0;
}
