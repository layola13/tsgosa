function main(): i32 {
  console.log(Date.now() > 0 ? 1 : 0);
  const d = new Date(0);
  console.log(d.getTime() === 0 ? 1 : 0);
  return 0;
}
