class C { s: string = ""; constructor(a: string[]) { this.s = a[1]; } }
function main(): i32 {
  const c = new C(["ab", "cd"]);
  console.log(c.s);
  return 0;
}
