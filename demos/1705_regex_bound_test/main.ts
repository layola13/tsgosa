function main(): i32 {
  const re = /(a+)(b+)/;
  console.log(re.test("xxaabbyy") ? 1 : 0);
  console.log(re.test("zz") ? 1 : 0);
  return 0;
}
