const greeting = "hi";
let topic = "sa";

function shout(x: string): void {
  console.log(x + "!");
}

console.log(greeting);
console.log(greeting + "-" + topic);
console.log(greeting.length);
if (greeting == "hi") {
  console.log(1);
}
shout(topic);
