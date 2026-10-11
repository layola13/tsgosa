class C { n: i32 = 0; constructor(a: i32[]) { this.n = a[2]; } }
function main(): i32 {
  const c = new C([5, 6, 7]);
  console.log(c.n);
  return 0;
}
