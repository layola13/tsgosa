class C {
  v: i32;
  constructor(v: i32) { this.v = v; }
  #dbl(): i32 { return this.v * 2; }
  run(): i32 { return this.#dbl(); }
}
function main(): i32 {
  const c = new C(21);
  console.log(c.run());
  return 0;
}
