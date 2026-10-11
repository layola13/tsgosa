class C { n: i32 = 0; s: string = ""; constructor(a: string[]) { this.n = a.length; this.s = a[0]; } }
function main(): i32 {
  const c = new C(["ab", "cd"]);
  console.log(c.s);
  console.log(c.n);
  return 0;
}
