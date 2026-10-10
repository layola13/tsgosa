function main(): i32 {
  const re = /[0-9]+/;
  console.log(re.exec("abc123")[0]);
  console.log(re.exec("abc123").length);
  return 0;
}
