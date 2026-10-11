class C { s: string = ""; constructor(f: boolean) { this.s = f ? "a" : "b"; } }
function main(): i32 {
  const c = new C(false);
  console.log(c.s);
  return 0;
}
