class C {
  v: i32;
  constructor(v: i32) { this.v = v; }
  run(): i32 {
    const f = () => this.v + 1;
    return f();
  }
}
function main(): i32 {
  const c = new C(41);
  console.log(c.run());
  return 0;
}
