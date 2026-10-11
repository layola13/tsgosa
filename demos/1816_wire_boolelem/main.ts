class C { ok: boolean = false; constructor(a: boolean[]) { this.ok = a[0]; } }
function main(): i32 {
  const c = new C([true, false]);
  console.log(c.ok);
  return 0;
}
