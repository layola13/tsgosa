class C { s: string = ""; constructor(a: Array<string>) { this.s = a[0]; } }
function main(): i32 {
  const c = new C(["ab", "cd"]);
  console.log(c.s);
  return 0;
}
