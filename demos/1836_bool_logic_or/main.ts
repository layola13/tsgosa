function main(): i32 {
  const f = false;
  const g = f || true;
  console.log(g ? 1 : 0);
  return 0;
}
