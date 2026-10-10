class C {
  v: i32;
  constructor(v: i32) { this.v = v; }
  get doubled(): i32 { return this.v * 2; }
}
function main(): i32 {
  const c = new C(5);
  console.log(c.doubled);
  console.log(c.doubled);
  return 0;
}
