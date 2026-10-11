class C { s: string = ""; constructor(a: string[], k: i32) { this.s = a[k]; } }
function main(): i32 {
  const c = new C(["ab", "cd"], 1);
  console.log(c.s);
  return 0;
}
