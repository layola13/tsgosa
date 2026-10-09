function main(): i32 {
  const t: string | null = null;
  console.log(t?.length);
  let u: string | null = null;
  u = "ab";
  console.log(u?.length);
  console.log(u.length);
  console.log(u[1]);
  return 0;
}
