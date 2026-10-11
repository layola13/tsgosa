class C { ok: boolean = false; constructor(f: boolean, g: boolean) { this.ok = f && g; } }
function main(): i32 {
  const c = new C(true, false);
  console.log(c.ok);
  return 0;
}
