class C { n: i32 = 0; constructor(a: Array<i32>) { this.n = a[0]; } }
function main(): i32 {
  const c = new C([9, 9]);
  console.log(c.n);
  return 0;
}
