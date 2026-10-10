class C {
  static K = 7;
  v: i32;
  constructor(v: i32) { this.v = v; }
}
function main(): i32 {
  const c = new C(5);
  console.log(c.v + C.K);
  return 0;
}
