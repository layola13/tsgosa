function main(): i32 {
  const r: i32 = 1;
  const w: i32 = 2;
  const x: i32 = 4;
  const perm: i32 = r | w | x;
  console.log(perm & w, perm & 8, perm ^ w);
  return 0;
}
