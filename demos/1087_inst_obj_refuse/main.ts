class A {
  v: i32;
  constructor(v: i32) { this.v = v; }
}
function main(): i32 {
  const a = new A(1);
  if (a instanceof Promise) { console.log(1); } else { console.log(0); }
  return 0;
}
