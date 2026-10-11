function main(): i32 {
  const re = /(a+)(b+)/;
  console.log(re.exec("xxaabbyy")[0]);
  return 0;
}
