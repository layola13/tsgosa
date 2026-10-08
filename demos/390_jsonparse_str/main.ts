interface P {
  name: string;
  age: i32;
}
function main(): i32 {
  const p: P = JSON.parse("{\"name\":\"ab\",\"age\":3}");
  console.log(p.age + p.name.length);
  return 0;
}
