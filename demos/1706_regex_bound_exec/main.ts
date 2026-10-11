function main(): i32 {
  const re = /ab+c/;
  console.log(re.exec("xxabbbcyy")[0]);
  return 0;
}
