class C { s: string = ""; constructor(a: string[]) { this.s = a[0] + a[1]; } }
function main(): i32 {
  const c = new C(["x", "y"]);
  console.log(c.s);
  return 0;
}
