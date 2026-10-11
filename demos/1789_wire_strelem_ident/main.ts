class C { s: string = ""; constructor(a: string[]) { this.s = a[0]; } }
function main(): i32 {
  const sa = ["ab", "cd"];
  const c = new C(sa);
  console.log(c.s);
  return 0;
}
