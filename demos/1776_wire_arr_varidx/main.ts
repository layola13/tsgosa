class C { n: i32 = 0; constructor(a: i32[], k: i32) { this.n = a[k]; } }
function main(): i32 {
  const c = new C([5, 6], 1);
  console.log(c.n);
  return 0;
}
