class C { s: string = ""; constructor(a: string[]) { this.s = "p-" + a[0]; } }
function main(): i32 {
  const c = new C(["ab", "cd"]);
  console.log(c.s);
  return 0;
}
