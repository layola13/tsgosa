class C { s: string = ""; t: string = ""; constructor(a: string[]) { this.s = a[0]; this.t = a[1]; } }
function main(): i32 {
  const c = new C(["ab", "cd"]);
  console.log(c.s + c.t);
  return 0;
}
