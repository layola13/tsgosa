class C { n: i32 = 0; constructor(k: i32) { this.n = k; } }
function main(): i32 {
  const c = new C(41);
  console.log(c.n);
  return 0;
}
