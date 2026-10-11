function main(): i32 {
  console.log(/(a+)(b+)/.exec("xxaabbyy")[0]);
  return 0;
}
