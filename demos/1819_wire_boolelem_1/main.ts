class C { ok: boolean = false; constructor(a: boolean[]) { this.ok = a[1]; } }
function main(): i32 {
  const c = new C([false, true]);
  console.log(c.ok);
  return 0;
}
