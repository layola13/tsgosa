class C { n: i32 = 0; constructor(k: i32) { this.n = k + 2; } }
function main(): i32 {
  const c = new C(40);
  console.log(c.n);
  return 0;
}
