function main(): i32 {
  const d = new Date();
  console.log(d.getFullYear() > 2000 ? 1 : 0);
  console.log(d.getMonth() >= 0 ? 1 : 0);
  console.log(d.getMonth() < 12 ? 1 : 0);
  console.log(d.getDate() >= 1 ? 1 : 0);
  return 0;
}
