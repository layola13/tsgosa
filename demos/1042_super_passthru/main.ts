class A {
  x: i32;
  y: i32;
  constructor(x: i32, y: i32) { this.x = x; this.y = y; }
}
class B extends A {
  constructor(a: i32, b: i32) { super(a, b); }
}
function main(): i32 {
  const o = new B(3, 4);
  console.log(o.x + o.y);
  return 0;
}
