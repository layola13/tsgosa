function probe(): i32 {
  const d = new Date();
  return d.getFullYear() > 2000;
}
function main(): i32 {
  console.log(probe());
  return 0;
}
