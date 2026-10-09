function main(): i32 {
  let a: string | null = null;
  a = "ab";
  console.log(a.length);
  const t: string | null = null;
  console.log(t?.length);
  let u: string | null = null;
  if (a.length == 2) {
    u = "xy";
  }
  console.log(u?.length);
  return 0;
}
