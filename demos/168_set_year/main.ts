function probe(): i32 {
  const d = new Date();
  d.setFullYear(2030);
  return d.getFullYear();
}
function main(): i32 {
  console.log(probe());
  return 0;
}